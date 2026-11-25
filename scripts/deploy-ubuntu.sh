#!/usr/bin/env bash
#
# Deploys GotATicket on an Ubuntu server (22.04, 24.04 or 26.04; amd64 or arm64), without
# Docker. It uses the MySQL database and Redis server you already have, and puts nginx in
# front of the app.
#
# Run it with no options and it works out the rest:
#
#   sudo ./scripts/deploy-ubuntu.sh             # http://<server ip>, sandbox payments
#
# Later, with a domain pointing at the server (HTTPS from Let's Encrypt, live payments):
#
#   sudo ./scripts/deploy-ubuntu.sh --domain tickets.example.co.za --email you@example.co.za \
#        --ozow
#
# What it does, safely re-runnable:
#   1. Installs nginx, plus Go and Node.js to build the app (MySQL and Redis are not touched)
#   2. Reads the database login from the settings you already have (see find_database)
#   3. Writes /opt/gotaticket/.env, keeping every value already in it
#   4. Builds a new release, migrates the database, switches to it and restarts the
#      gotaticket-web and gotaticket-worker services, then waits until the site is healthy
#   5. Configures nginx (and HTTPS with a domain), opens SSH and HTTP(S) in the firewall
#   6. Sets up daily backups of the database, uploaded images and settings

set -Eeuo pipefail

readonly PROJECT=gotaticket
readonly SERVICE_USER=$PROJECT
readonly APP_DIR=/opt/$PROJECT
readonly ENV_FILE=$APP_DIR/.env
readonly DB_CNF=$APP_DIR/.mysql.cnf
readonly DATA_DIR=/var/lib/$PROJECT
readonly BACKUP_DIR=/var/backups/$PROJECT
readonly NGINX_SITE=/etc/nginx/sites-available/$PROJECT
readonly MIN_GO=1.24.0
readonly MIN_NODE=20.19.0
readonly NODE_MAJOR=22
readonly KEEP_RELEASES=3
export PATH=/usr/local/go/bin:/usr/local/bin:$PATH

DOMAIN=""
HOST_IP=""
PORT=""     # where nginx listens (no domain); default 80
APP_PORT="" # where the app listens on 127.0.0.1, behind nginx; default 8000
EMAIL=""
ADMIN_EMAIL=""
OZOW=false
REPO=""
SOURCE=""
BRANCH=main
STAGING=false
DEMO=false
FIREWALL=true
SRC_PATH="" # the folder the code was copied from (its .env files may hold the database login)
RELEASE=""
HTTPS_READY=false
WWW=false    # also serve www.<domain>, when its DNS points here too
SERVER_IP=""

# ---- Output helpers -----------------------------------------------------------------------

if [[ -t 1 ]]; then
  BOLD=$'\e[1m' GREEN=$'\e[32m' YELLOW=$'\e[33m' RED=$'\e[31m' RESET=$'\e[0m'
else
  BOLD="" GREEN="" YELLOW="" RED="" RESET=""
fi

step() { printf '\n%s==> %s%s\n' "$BOLD" "$*" "$RESET"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '%sWarning:%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die() {
  printf '%sError:%s %s\n' "$RED" "$RESET" "$*" >&2
  exit 1
}

trap 'die "line $LINENO: \"$BASH_COMMAND\" failed (exit $?)"' ERR

usage() {
  cat <<EOF
Deploys GotATicket on this Ubuntu server with nginx, using your existing MySQL and Redis.

Usage: sudo $0 [options]

With no options the site runs on http://<this server's IP> as a test site.

Address:
  --ip IP                The server's public IPv4 address (default: detected)
  --port PORT            Port nginx listens on without a domain (default: 80)
  --app-port PORT        Port the app listens on, on 127.0.0.1 only (default: 8000)
  --domain DOMAIN        e.g. tickets.example.co.za; adds HTTPS from Let's Encrypt.
                         Its DNS A record must point here, and --email is required

Payments:
  --ozow                 Enable live Ozow payments using credentials in $ENV_FILE.
                         Requires a domain and OZOW_CLIENT_ID, OZOW_CLIENT_SECRET,
                         OZOW_SITE_CODE and OZOW_WEBHOOK_SECRET.
  --staging              With a domain: a test site with sandbox payments

Other:
  --email EMAIL          Your email: Let's Encrypt notices and the first super administrator
  --admin-email EMAIL    First super administrator (default: --email, else
                         admin@$PROJECT.local)
  --demo                 Load demo events, orders and accounts (test sites only)
  --source PATH          The project folder or its .zip (default: the folder this script is
                         in, or a GotATicket .zip next to it or in the current folder)
  --repo URL             Clone or update the code from git instead
  --branch NAME          Git branch to deploy (default: main)
  --no-firewall          Leave the firewall (ufw) as it is
  -h, --help             Show this help

Database: the login comes from DATABASE_DSN in $ENV_FILE, or else from the project's
.env or backend/.env (DATABASE_DSN, DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME, or
MYSQL_USER/MYSQL_PASSWORD/MYSQL_DATABASE). Nothing in MySQL is created or changed except
the database itself when it doesn't exist yet and the user may create it.
EOF
}

# ---- Arguments ----------------------------------------------------------------------------

valid_port() { [[ $1 =~ ^[0-9]{1,5}$ ]] && ((10#$1 >= 1 && 10#$1 <= 65535)); }

valid_ipv4() {
  local octet re='^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$'
  [[ $1 =~ $re ]] || return 1
  for octet in ${1//./ }; do
    ((10#$octet <= 255)) || return 1
  done
}

has_domain() { [[ -n $DOMAIN ]]; }

parse_args() {
  while (($#)); do
    case "$1" in
      --domain) DOMAIN="${2:?--domain needs a value}"; shift 2 ;;
      --ip) HOST_IP="${2:?--ip needs a value}"; shift 2 ;;
      --port) PORT="${2:?--port needs a value}"; shift 2 ;;
      --app-port) APP_PORT="${2:?--app-port needs a value}"; shift 2 ;;
      --email) EMAIL="${2:?--email needs a value}"; shift 2 ;;
      --admin-email) ADMIN_EMAIL="${2:?--admin-email needs a value}"; shift 2 ;;
      --ozow) OZOW=true; shift ;;
      --repo) REPO="${2:?--repo needs a value}"; shift 2 ;;
      --source) SOURCE="${2:?--source needs a value}"; shift 2 ;;
      --branch) BRANCH="${2:?--branch needs a value}"; shift 2 ;;
      --staging) STAGING=true; shift ;;
      --demo) DEMO=true; shift ;;
      --no-firewall) FIREWALL=false; shift ;;
      -h | --help) usage; exit 0 ;;
      *) usage >&2; die "unknown option: $1" ;;
    esac
  done

  if [[ -n $PORT ]]; then
    valid_port "$PORT" || die "--port must be a number from 1 to 65535"
    PORT=$((10#$PORT))
  fi
  if [[ -n $APP_PORT ]]; then
    valid_port "$APP_PORT" || die "--app-port must be a number from 1 to 65535"
    APP_PORT=$((10#$APP_PORT))
  fi
  if [[ -n $EMAIL ]]; then
    [[ $EMAIL =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "--email is not a valid email address"
  fi

  if has_domain; then
    [[ -z $HOST_IP ]] || die "use either --domain or --ip, not both"
    [[ -z $PORT ]] || die "--port is for a site without a domain; with a domain nginx uses 80 and 443"
    [[ $DOMAIN =~ ^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$ ]] ||
      die "--domain must be a domain name such as tickets.example.co.za (no https:// or path)"
    [[ -n $EMAIL ]] || { usage >&2; die "--email is required with --domain"; }
    if $STAGING && $OZOW; then
      die "use either --staging or --ozow, not both"
    fi
    if $DEMO && ! $STAGING; then
      die "--demo needs --staging on a domain; demo data must never reach a live site"
    fi
  else
    # No HTTPS without a domain: passwords would travel unencrypted, so this is always a
    # test site and live keys are refused.
    STAGING=true
    if $OZOW; then
      die "Ozow payments need a domain with HTTPS; use --domain"
    fi
    if [[ -n $HOST_IP ]]; then
      valid_ipv4 "$HOST_IP" || die "--ip must be an IPv4 address such as 102.165.10.20"
    fi
  fi
  ADMIN_EMAIL="${ADMIN_EMAIL:-${EMAIL:-admin@$PROJECT.local}}"
}

# ---- System checks ------------------------------------------------------------------------

check_system() {
  step "Checking the server"
  [[ $EUID -eq 0 ]] || die "run this script as root (sudo $0 ...)"
  [[ -d /run/systemd/system ]] || die "this server doesn't run systemd, which the services need"
  [[ -r /etc/os-release ]] || die "cannot read /etc/os-release"
  # shellcheck source=/dev/null
  . /etc/os-release
  [[ ${ID:-} == ubuntu ]] || die "this script supports Ubuntu only (found ${ID:-unknown})"
  case "${VERSION_ID:-}" in
    22.04 | 24.04 | 26.04) ;;
    *) warn "tested on Ubuntu 22.04, 24.04 and 26.04; found ${VERSION_ID:-unknown}, continuing anyway" ;;
  esac
  case "$(dpkg --print-architecture)" in
    amd64 | arm64) ;;
    *) die "only amd64 and arm64 servers are supported" ;;
  esac
  info "Ubuntu ${VERSION_ID:-?} ($(dpkg --print-architecture)), $(nproc) CPU, $(free -h | awk '/^Mem:/ {print $2}') RAM"

  local free_gb
  free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  ((free_gb >= 5)) || warn "only ${free_gb} GB free on /; 5 GB or more is recommended"
}

ensure_swap() {
  local mem_mb swap_mb
  mem_mb=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
  swap_mb=$(awk '/^SwapTotal:/ {print int($2 / 1024)}' /proc/meminfo)
  if ((mem_mb >= 3500 || swap_mb > 0)); then
    return
  fi
  step "Adding a 2 GB swap file so the build doesn't run out of memory ($mem_mb MB RAM)"
  fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048 status=none
  chmod 600 /swapfile
  mkswap /swapfile >/dev/null
  swapon /swapfile
  grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >>/etc/fstab
}

# ---- Packages -----------------------------------------------------------------------------

apt_install() {
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "$@" >/dev/null
}

version_at_least() { # version_at_least HAVE NEED
  [[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" == "$2" ]]
}

install_packages() {
  step "Installing nginx and build tools"
  apt-get update -qq
  local packages=(ca-certificates curl gnupg git rsync openssl ufw cron jq xz-utils tar unzip iproute2 nginx)
  # Only the MySQL command-line client, to check the login and make backups; never a server.
  if ! command -v mysql >/dev/null || ! command -v mysqldump >/dev/null; then
    packages+=(default-mysql-client)
  fi
  command -v redis-cli >/dev/null || packages+=(redis-tools)
  has_domain && packages+=(certbot python3-certbot-nginx)
  apt_install "${packages[@]}"
  systemctl enable --now nginx >/dev/null 2>&1
  info "$(nginx -v 2>&1 | sed 's|nginx version: ||')"
}

# Go from go.dev, verified against the published SHA-256. Ubuntu's own Go is too old.
install_go() {
  local have=""
  command -v go >/dev/null && have=$(go env GOVERSION | sed 's/^go//')
  if [[ -n $have ]] && version_at_least "$have" "$MIN_GO"; then
    info "Go $have"
    return
  fi
  step "Installing Go"
  local arch file sha tmp
  arch=$(dpkg --print-architecture)
  read -r file sha < <(curl -fsSL 'https://go.dev/dl/?mode=json' |
    jq -r --arg a "$arch" '[.[] | select(.stable)][0].files[]
      | select(.os == "linux" and .arch == $a and .kind == "archive") | "\(.filename) \(.sha256)"')
  [[ -n ${file:-} && -n ${sha:-} ]] || die "could not find a Go download for $arch"
  tmp=$(mktemp -d)
  curl -fsSL "https://go.dev/dl/$file" -o "$tmp/$file"
  echo "$sha  $tmp/$file" | sha256sum -c --quiet - || die "the Go download failed its checksum"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tmp/$file"
  rm -rf "$tmp"
  info "$(go version)"
}

# Node.js LTS from nodejs.org, verified against SHASUMS256.txt. Ubuntu's own is too old.
install_node() {
  local have=""
  command -v node >/dev/null && have=$(node --version | sed 's/^v//')
  if [[ -n $have ]] && version_at_least "$have" "$MIN_NODE"; then
    info "Node.js $have"
    return
  fi
  step "Installing Node.js $NODE_MAJOR"
  local arch base file sums tmp
  arch=$(dpkg --print-architecture)
  [[ $arch == amd64 ]] && arch=x64
  base="https://nodejs.org/dist/latest-v$NODE_MAJOR.x"
  sums=$(curl -fsSL "$base/SHASUMS256.txt")
  file=$(awk -v a="linux-$arch.tar.xz" '$2 ~ a"$" {print $2; exit}' <<<"$sums")
  [[ -n $file ]] || die "could not find a Node.js download for $arch"
  tmp=$(mktemp -d)
  curl -fsSL "$base/$file" -o "$tmp/$file"
  (cd "$tmp" && grep " $file\$" <<<"$sums" | sha256sum -c --quiet -) || die "the Node.js download failed its checksum"
  rm -rf /usr/local/lib/nodejs
  mkdir -p /usr/local/lib/nodejs
  tar -C /usr/local/lib/nodejs --strip-components=1 -xJf "$tmp/$file"
  rm -rf "$tmp"
  local bin
  for bin in node npm npx; do
    ln -sfn "/usr/local/lib/nodejs/bin/$bin" "/usr/local/bin/$bin"
  done
  info "Node.js $(node --version)"
}

# ---- Address, ports and firewall ----------------------------------------------------------

# The address people type in their browser: the public IP, or the first local one on a
# server without a public address (for example inside an office network).
detect_ip() {
  [[ -n $HOST_IP ]] && return
  HOST_IP=$(curl -4fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  valid_ipv4 "$HOST_IP" || HOST_IP=$(hostname -I | awk '{print $1}')
  valid_ipv4 "$HOST_IP" || die "could not work out this server's IP address; pass it with --ip"
  info "Using IP address $HOST_IP (pass --ip to choose another)"
}

# Ports from the options, else from an earlier run, else the defaults.
resolve_ports() {
  if [[ -z $APP_PORT && -f $ENV_FILE ]]; then
    local addr
    addr=$(get_env HTTP_ADDR)
    [[ ${addr##*:} =~ ^[0-9]+$ ]] && APP_PORT=${addr##*:}
  fi
  APP_PORT=${APP_PORT:-8000}
  if ! has_domain && [[ -z $PORT && -f $NGINX_SITE ]]; then
    PORT=$(awk '$1 == "listen" && $2 ~ /^[0-9]+/ {sub(/;$/, "", $2); print $2; exit}' "$NGINX_SITE")
  fi
  PORT=${PORT:-80}
  if ! has_domain && ((PORT == APP_PORT)); then
    die "--port and --app-port must differ (both are $PORT)"
  fi
}

# port_owner PORT: the program listening on PORT, or nothing.
port_owner() {
  ss -Hltnp "sport = :$1" 2>/dev/null | grep -o 'users:(("[^"]*"' | head -1 | cut -d'"' -f2 || true
}

# Stops before building when another program holds a port this site needs.
check_ports() {
  local owner port
  owner=$(port_owner "$APP_PORT")
  if [[ -n $owner && $owner != "$PROJECT" ]]; then
    die "port $APP_PORT, which the app listens on, is already used by \"$owner\".
       Stop that program, or choose another port with --app-port."
  fi
  for port in $(has_domain && echo "80 443" || echo "$PORT"); do
    owner=$(port_owner "$port")
    if [[ -n $owner && $owner != nginx ]]; then
      die "port $port is already used by \"$owner\"; nginx needs it.
       Stop that program, or choose another port with --port."
    fi
  done
}

configure_firewall() {
  if ! $FIREWALL; then
    info "Skipping the firewall (--no-firewall)"
    return
  fi
  step "Configuring the firewall"
  # Allow every port sshd listens on before enabling ufw, so this session isn't cut off.
  local ssh_ports port site_ports
  ssh_ports=$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2}' || true)
  for port in ${ssh_ports:-22}; do
    ufw allow "$port/tcp" comment 'SSH' >/dev/null
  done
  if has_domain; then
    ufw allow 80/tcp comment 'HTTP' >/dev/null
    ufw allow 443/tcp comment 'HTTPS' >/dev/null
    site_ports="80, 443"
  else
    ufw allow "$PORT/tcp" comment 'GotATicket' >/dev/null
    site_ports="$PORT"
  fi
  ufw --force enable >/dev/null
  info "Open: SSH (${ssh_ports:-22}), $site_ports. The app itself listens on 127.0.0.1:$APP_PORT only."
  info "If your hosting provider has its own firewall or security group, open $site_ports there too."
}

# ---- Code ---------------------------------------------------------------------------------

fetch_source() {
  local src_dir=$APP_DIR/src
  step "Getting the code into $src_dir"
  mkdir -p "$src_dir"

  if [[ -n $REPO ]]; then
    if [[ -d $src_dir/.git ]]; then
      git -C "$src_dir" remote set-url origin "$REPO"
      git -C "$src_dir" fetch --quiet origin "$BRANCH"
      git -C "$src_dir" checkout --quiet "$BRANCH"
      git -C "$src_dir" reset --quiet --hard "origin/$BRANCH"
    else
      rm -rf "$src_dir"
      git clone --quiet --branch "$BRANCH" "$REPO" "$src_dir"
    fi
    info "Commit $(git -C "$src_dir" log -1 --format='%h %s')"
    return
  fi

  SRC_PATH=$(find_source) || exit 1 # find_source has already said why
  is_project "$SRC_PATH" || die "could not find the project folder"
  if [[ $SRC_PATH != "$src_dir" ]]; then
    rsync -a --delete \
      --exclude '.env' --exclude '.git/' --exclude 'node_modules/' \
      --exclude 'frontend/dist/' --exclude 'backend/storage/' --exclude 'backend/bin/' \
      "$SRC_PATH/" "$src_dir/"
  fi
  info "Copied from $SRC_PATH"
}

is_project() { [[ -f $1/backend/go.mod && -f $1/frontend/package.json ]]; }

# Prints the project folder to deploy: --source (a folder or a .zip), the folder this script
# sits in, or a GotATicket zip next to the script or in the current folder.
find_source() {
  local script_dir candidate
  script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

  if [[ -n $SOURCE ]]; then
    if [[ -d $SOURCE ]]; then
      is_project "$SOURCE" || die "$SOURCE is not the GotATicket project folder (no backend/ and frontend/)"
      (cd "$SOURCE" && pwd)
      return
    fi
    [[ -f $SOURCE ]] || die "--source $SOURCE does not exist"
    extract_zip "$SOURCE"
    return
  fi

  is_project "$script_dir/.." && { (cd "$script_dir/.." && pwd); return; }
  is_project "$script_dir" && { echo "$script_dir"; return; }

  candidate=$(find "$script_dir" "$PWD" -maxdepth 1 -type f -iname 'gotaticket*.zip' -printf '%T@ %p\n' 2>/dev/null |
    sort -rn | head -1 | cut -d' ' -f2-)
  [[ -n $candidate ]] ||
    die "could not find the code. Unzip it and run the script from inside it:
         unzip gotaticket.zip && cd gotaticket && sudo ./deploy.sh
       or pass --source /path/to/gotaticket.zip"
  extract_zip "$candidate"
}

# Unzips into a fresh folder and prints the project folder inside it.
extract_zip() {
  local zip=$1 dir root
  dir=$APP_DIR/upload
  rm -rf "$dir"
  mkdir -p "$dir"
  unzip -q "$zip" -d "$dir" || die "could not unzip $zip"
  root=$(find "$dir" -maxdepth 3 -type f -path '*/backend/go.mod' -printf '%h\n' | head -1)
  root=${root%/backend}
  if [[ -z $root ]] || ! is_project "$root"; then
    die "$zip does not contain the GotATicket project"
  fi
  info "Unzipped $(basename "$zip")" >&2
  echo "$root"
}

# ---- Settings file ------------------------------------------------------------------------

# set_env KEY VALUE: replaces KEY=... in .env, or appends it. Values are written as they are,
# so keep comments on their own lines (the app strips " #..." from unquoted values).
set_env() {
  local tmp
  tmp=$(mktemp "$ENV_FILE.XXXXXX")
  KEY="$1" VALUE="$2" awk '
    BEGIN { key = ENVIRON["KEY"]; line = key "=" ENVIRON["VALUE"] }
    index($0, key "=") == 1 { if (!done) print line; done = 1; next }
    { print }
    END { if (!done) print line }
  ' "$ENV_FILE" >"$tmp"
  mv "$tmp" "$ENV_FILE"
  secure_env_file
}

# get_env KEY [FILE]: the value, without surrounding quotes or an inline " #" comment.
get_env() {
  local file=${2:-$ENV_FILE} value
  [[ -f $file ]] || return 0
  value=$(awk -v key="$1" '{ sub(/^export[ \t]+/, "") } index($0, key "=") == 1 { print substr($0, length(key) + 2); exit }' "$file")
  value=${value%$'\r'}
  if [[ $value =~ ^\"(.*)\"$ || $value =~ ^\'(.*)\'$ ]]; then
    value=${BASH_REMATCH[1]}
  elif [[ $value == *" #"* ]]; then
    value=${value%% #*}
  fi
  printf '%s' "$value"
}

secure_env_file() {
  chown "root:$SERVICE_USER" "$ENV_FILE"
  chmod 640 "$ENV_FILE"
}

random_secret() { openssl rand -base64 48 | tr -d '\n/+='; }
random_password() { openssl rand -hex 16; }

create_service_user() {
  if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
  fi
  install -d -m 750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR" "$DATA_DIR/uploads" "$DATA_DIR/mail"
  install -d -m 755 "$APP_DIR" "$APP_DIR/releases"
}

write_env_template() { # write_env_template FILE
  cat >"$1" <<EOF
# GotATicket settings, read by the gotaticket-web and gotaticket-worker services.
# After a change, apply it with: sudo systemctl restart gotaticket-web gotaticket-worker
# Keep comments on their own lines. Never change QR_SIGNING_KEY: every ticket already
# issued would stop scanning.

APP_NAME=GotATicket
APP_ENV=staging
HTTP_ADDR=127.0.0.1:8000
PUBLIC_URL=https://gotaticket.co.za
API_URL=http://localhost
LOG_FORMAT=json

DATABASE_DSN=
REDIS_URL=
AUTO_MIGRATE=false
EMBEDDED_WORKER=false

JWT_SECRET=
QR_SIGNING_KEY=
COOKIE_SECURE=false
TRUST_PROXY_HEADERS=true
PASSWORD_HASH_ITERATIONS=600000

# First super administrator, created on first start. Remove the password once you have
# signed in and changed it.
BOOTSTRAP_ADMIN_EMAIL=
BOOTSTRAP_ADMIN_PASSWORD=

# sandbox = built-in test gateway; ozow = live payments (required in production).
PAYMENT_PROVIDER=sandbox
OZOW_CLIENT_ID=
OZOW_CLIENT_SECRET=
OZOW_SITE_CODE=
OZOW_WEBHOOK_SECRET=
OZOW_BASE_URL=https://one.ozow.com/v1
SANDBOX_WEBHOOK_SECRET=

# Bank details shown for EFT payments.
EFT_ENABLED=true
EFT_BANK_NAME=First National Bank
EFT_ACCOUNT_NAME=GotATicket (Pty) Ltd
EFT_ACCOUNT_NUMBER=62000000000
EFT_BRANCH_CODE=250655

# Email. With SMTP_HOST empty, emails are saved as files in MAIL_DIR instead of sent.
MAIL_FROM=GotATicket <no-reply@$PROJECT.local>
SMTP_HOST=
SMTP_PORT=587
SMTP_USERNAME=
SMTP_PASSWORD=
# none, starttls or tls
SMTP_TLS=starttls

# Sign in with Google: redirect URI <PUBLIC_URL>/api/v1/auth/google/callback (needs a domain)
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=

# AI features stay off until DEEPSEEK_API_KEY is set.
DEEPSEEK_API_KEY=
AI_BASE_URL=https://api.deepseek.com
AI_MODEL=deepseek-flash
AI_REVIEW_MODEL=deepseek-v4-pro
AI_DAILY_TOKEN_LIMIT=2000000

# Files
WEB_DIR=$APP_DIR/current/web
STORAGE_DIR=$DATA_DIR/uploads
MAIL_DIR=$DATA_DIR/mail
EOF
}

# An existing .env may come from an older version of this script or from the Docker setup,
# which leaves out settings this server needs. Adds each missing setting with its default
# and never touches the ones already there.
add_missing_settings() {
  local template line key added=()
  template=$(mktemp)
  write_env_template "$template"
  while IFS= read -r line; do
    [[ $line =~ ^([A-Z0-9_]+)= ]] || continue
    key=${BASH_REMATCH[1]}
    if ! grep -q "^$key=" "$ENV_FILE"; then
      printf '%s\n' "$line" >>"$ENV_FILE"
      added+=("$key")
    fi
  done <"$template"
  rm -f "$template"
  ((${#added[@]} == 0)) || info "Added missing settings: ${added[*]}"
}

write_env() {
  step "Writing the settings ($ENV_FILE)"
  local first_run=false
  if [[ ! -f $ENV_FILE ]]; then
    write_env_template "$ENV_FILE"
    first_run=true
  else
    cp -p "$ENV_FILE" "$ENV_FILE.bak"
    add_missing_settings
  fi
  secure_env_file

  if has_domain && ! $STAGING && { $OZOW || [[ "$(get_env PAYMENT_PROVIDER)" == ozow ]]; }; then
    refuse_demo_data_in_production
  fi

  # Secrets are generated once and never replaced: a new QR_SIGNING_KEY would invalidate
  # every ticket already issued.
  local key
  for key in JWT_SECRET QR_SIGNING_KEY SANDBOX_WEBHOOK_SECRET; do
    [[ -n "$(get_env "$key")" ]] || set_env "$key" "$(random_secret)"
  done
  # The well-known password from the Docker example must never protect a server.
  if [[ -z "$(get_env BOOTSTRAP_ADMIN_PASSWORD)" && $first_run == true ]] ||
    [[ "$(get_env BOOTSTRAP_ADMIN_PASSWORD)" == ChangeMe2026 ]]; then
    set_env BOOTSTRAP_ADMIN_PASSWORD "$(random_password)"
  fi
  [[ -n "$(get_env BOOTSTRAP_ADMIN_EMAIL)" ]] || set_env BOOTSTRAP_ADMIN_EMAIL "$ADMIN_EMAIL"

  # Settings this script owns, put right on every run so the services can always start.
  set_env AUTO_MIGRATE false # the script migrates before each release
  set_env EMBEDDED_WORKER false # gotaticket-worker runs the background jobs
  set_env WEB_DIR "$APP_DIR/current/web"
  set_env STORAGE_DIR "$DATA_DIR/uploads"
  set_env MAIL_DIR "$DATA_DIR/mail"
  set_env HTTP_ADDR "127.0.0.1:$APP_PORT" # only nginx reaches the app
  set_env TRUST_PROXY_HEADERS true # nginx passes the client's address in X-Forwarded-For
  if [[ "$(get_env SMTP_HOST)" == mailpit ]]; then # Docker's test inbox doesn't exist here
    set_env SMTP_HOST ""
    set_env SMTP_PORT 587
    set_env SMTP_TLS starttls
  fi

  local site
  if has_domain; then
    site="https://$DOMAIN"
    set_env COOKIE_SECURE true
    if [[ "$(get_env MAIL_FROM)" == *"@$PROJECT.local>" ]]; then
      set_env MAIL_FROM "GotATicket <no-reply@$DOMAIN>"
    fi
  else
    site="http://$HOST_IP"
    ((PORT == 80)) || site="$site:$PORT"
    set_env COOKIE_SECURE false # browsers drop secure cookies on plain HTTP
  fi
  set_env PUBLIC_URL "$site"
  set_env API_URL "$site"

  if ! has_domain || $STAGING; then
    set_env APP_ENV staging
    set_env PAYMENT_PROVIDER sandbox
  elif $OZOW || [[ "$(get_env PAYMENT_PROVIDER)" == ozow ]]; then
    for key in OZOW_CLIENT_ID OZOW_CLIENT_SECRET OZOW_SITE_CODE OZOW_WEBHOOK_SECRET; do
      [[ -n "$(get_env "$key")" ]] || die "set $key in $ENV_FILE before enabling Ozow"
    done
    [[ "$(get_env OZOW_BASE_URL)" == https://one.ozow.com/v1 ]] ||
      die "live Ozow payments require OZOW_BASE_URL=https://one.ozow.com/v1"
    set_env APP_ENV production
    set_env PAYMENT_PROVIDER ozow
  elif [[ "$(get_env APP_ENV)" == staging ]]; then
    STAGING=true
    set_env PAYMENT_PROVIDER sandbox
  else
    die "configure Ozow credentials in $ENV_FILE and choose --ozow for a live site, or --staging for a test site"
  fi

  if [[ -z "$(get_env SMTP_HOST)" ]]; then
    warn "emails are saved as files in $DATA_DIR/mail, not sent. Set SMTP_HOST, SMTP_PORT," \
      "SMTP_USERNAME, SMTP_PASSWORD and SMTP_TLS in $ENV_FILE and run this script again."
  fi
  [[ -n "$(get_env DEEPSEEK_API_KEY)" ]] ||
    info "AI features are off until you set DEEPSEEK_API_KEY in $ENV_FILE."
}

refuse_demo_data_in_production() {
  [[ "$(get_env DEMO_DATA)" == true ]] || return 0
  die "this server holds demo events, orders and accounts, which must not go live. Empty
       the database and $DATA_DIR/uploads, remove the DEMO_DATA line from $ENV_FILE,
       then run this script again."
}

# ---- Database (your existing MySQL) -------------------------------------------------------

DB_USER="" DB_PASS="" DB_HOST="" DB_PORT="" DB_SOCKET="" DB_NAME="" DB_PARAMS=""

# parse_dsn DSN: splits a Go MySQL DSN, user:password@tcp(host:port)/name?params.
parse_dsn() {
  local re='^([^:@]*)(:(.*))?@(tcp|unix)\(([^)]*)\)/([^?]*)(\?.*)?$'
  [[ $1 =~ $re ]] || return 1
  DB_USER=${BASH_REMATCH[1]} DB_PASS=${BASH_REMATCH[3]} DB_NAME=${BASH_REMATCH[6]} DB_PARAMS=${BASH_REMATCH[7]}
  DB_HOST="" DB_PORT="" DB_SOCKET=""
  if [[ ${BASH_REMATCH[4]} == unix ]]; then
    DB_SOCKET=${BASH_REMATCH[5]}
  else
    DB_HOST=${BASH_REMATCH[5]%:*}
    DB_PORT=${BASH_REMATCH[5]##*:}
    [[ ${BASH_REMATCH[5]} == *:* ]] || DB_PORT=3306
    # "mysql" is the database's name inside Docker; without Docker it runs on this server.
    if [[ $DB_HOST == mysql ]]; then
      DB_HOST=127.0.0.1
      DB_PORT=3306
    fi
  fi
  [[ -n $DB_USER && -n $DB_NAME ]]
}

build_dsn() {
  local address
  if [[ -n $DB_SOCKET ]]; then
    address="unix($DB_SOCKET)"
  else
    address="tcp($DB_HOST:$DB_PORT)"
  fi
  printf '%s:%s@%s/%s%s' "$DB_USER" "$DB_PASS" "$address" "$DB_NAME" "$DB_PARAMS"
}

# Finds the database login: DATABASE_DSN in /opt/gotaticket/.env, else in the project's .env
# or backend/.env, else DB_* or MYSQL_* settings in any of those files.
find_database() {
  local file dsn
  dsn=$(get_env DATABASE_DSN)
  if [[ -n $dsn ]] && parse_dsn "$dsn"; then
    info "Database login from $ENV_FILE"
    return
  fi
  for file in "$ENV_FILE" ${SRC_PATH:+"$SRC_PATH/.env" "$SRC_PATH/backend/.env"}; do
    [[ -f $file ]] || continue
    dsn=$(get_env DATABASE_DSN "$file")
    if [[ -n $dsn ]] && parse_dsn "$dsn"; then
      info "Database login from DATABASE_DSN in $file"
      return
    fi
    local user pass
    user=$(get_env DB_USER "$file")
    [[ -n $user ]] || user=$(get_env MYSQL_USER "$file")
    pass=$(get_env DB_PASSWORD "$file")
    [[ -n $pass ]] || pass=$(get_env MYSQL_PASSWORD "$file")
    if [[ -n $user || -n $pass ]]; then
      DB_USER=${user:-$PROJECT}
      DB_PASS=$pass
      DB_HOST=$(get_env DB_HOST "$file")
      [[ -n $DB_HOST && $DB_HOST != mysql ]] || DB_HOST=127.0.0.1
      DB_PORT=$(get_env DB_PORT "$file")
      DB_PORT=${DB_PORT:-3306}
      DB_NAME=$(get_env DB_NAME "$file")
      [[ -n $DB_NAME ]] || DB_NAME=$(get_env MYSQL_DATABASE "$file")
      DB_NAME=${DB_NAME:-$PROJECT}
      DB_SOCKET="" DB_PARAMS=""
      info "Database login from the DB_/MYSQL_ settings in $file"
      return
    fi
  done
  die "no database login found. Add your MySQL login to $ENV_FILE as
         DATABASE_DSN=user:password@tcp(127.0.0.1:3306)/$PROJECT
       and run this script again."
}

# Option file for the mysql client, so the password never appears on a command line.
write_db_cnf() { # write_db_cnf FILE [with_database]
  local escaped=${DB_PASS//\\/\\\\}
  escaped=${escaped//\"/\\\"}
  (
    umask 077
    {
      echo "[client]"
      echo "user=\"$DB_USER\""
      echo "password=\"$escaped\""
      if [[ -n $DB_SOCKET ]]; then
        echo "socket=$DB_SOCKET"
      else
        echo "host=$DB_HOST"
        echo "port=$DB_PORT"
        echo "protocol=tcp"
      fi
    } >"$1"
  )
}

check_database() {
  step "Checking the MySQL database"
  find_database
  write_db_cnf "$DB_CNF"
  local where=${DB_SOCKET:-$DB_HOST:$DB_PORT} error
  if ! error=$(mysql --defaults-extra-file="$DB_CNF" -e 'SELECT 1' 2>&1 >/dev/null); then
    die "could not sign in to MySQL at $where as $DB_USER:
         $error
       Check the login in $ENV_FILE (DATABASE_DSN) and that MySQL is running."
  fi
  if ! mysql --defaults-extra-file="$DB_CNF" -e "USE \`$DB_NAME\`" 2>/dev/null; then
    mysql --defaults-extra-file="$DB_CNF" \
      -e "CREATE DATABASE \`$DB_NAME\` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci" 2>/dev/null ||
      die "database $DB_NAME doesn't exist and $DB_USER may not create it. As a MySQL
       administrator, run:
         CREATE DATABASE $DB_NAME CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
         GRANT ALL PRIVILEGES ON $DB_NAME.* TO '$DB_USER'@'localhost', '$DB_USER'@'127.0.0.1';"
    info "Created database $DB_NAME"
  fi
  set_env DATABASE_DSN "$(build_dsn)"
  info "Signed in to $DB_NAME at $where as $DB_USER"
}

check_redis() {
  local url
  url=$(get_env REDIS_URL)
  if [[ -n $url && $url != *"//redis:"* ]]; then
    if redis-cli -u "$url" ping 2>/dev/null | grep -q PONG; then
      info "Redis: $url"
    else
      warn "Redis at REDIS_URL doesn't answer; the app starts anyway and keeps its cache in memory until it does"
    fi
    return
  fi
  if redis-cli -h 127.0.0.1 -p 6379 ping 2>/dev/null | grep -q PONG; then
    set_env REDIS_URL redis://127.0.0.1:6379/0
    info "Redis: redis://127.0.0.1:6379/0"
  else
    set_env REDIS_URL ""
    warn "no Redis answered on 127.0.0.1:6379 (with a password, set REDIS_URL=redis://:password@127.0.0.1:6379/0" \
      "in $ENV_FILE). The app works without it, keeping its cache in memory."
  fi
}

# ---- Build and release --------------------------------------------------------------------

build_release() {
  local src=$APP_DIR/src version
  version=$(git -C "$src" rev-parse --short HEAD 2>/dev/null || echo local)
  RELEASE=$APP_DIR/releases/$(date +%Y%m%d-%H%M%S)-$version
  step "Building release $(basename "$RELEASE") (the first build takes a few minutes)"
  mkdir -p "$RELEASE"

  info "Go binary"
  (cd "$src/backend" && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$RELEASE/$PROJECT" "./cmd/$PROJECT")

  info "Web app"
  (
    cd "$src/frontend"
    npm ci --no-audit --no-fund --loglevel=error
    VITE_APP_NAME="$(get_env APP_NAME)" npm run build --silent
  ) >/dev/null
  cp -a "$src/frontend/dist" "$RELEASE/web"
  chmod -R a+rX "$RELEASE"
}

install_cli() {
  cat >/usr/local/bin/$PROJECT <<EOF
#!/usr/bin/env bash
# Runs a GotATicket command with the server's settings, as the service user, e.g.
#   sudo $PROJECT migrate      sudo $PROJECT seed
# GOTATICKET_BIN picks another binary (the deploy script migrates with the new release).
set -euo pipefail
[[ \$EUID -eq 0 ]] || exec sudo "\$0" "\$@"
cd $APP_DIR # the app reads .env from its working directory
exec runuser -u $SERVICE_USER -- "\${GOTATICKET_BIN:-$APP_DIR/current/$PROJECT}" "\$@"
EOF
  chmod 755 /usr/local/bin/$PROJECT
}

install_services() {
  write_unit web serve "website, portal and API"
  write_unit worker worker "background jobs: emails, reservation expiry, payment checks"
  systemctl daemon-reload
  systemctl enable "$PROJECT-web" "$PROJECT-worker" >/dev/null 2>&1
}

# write_unit ROLE COMMAND DESCRIPTION
write_unit() {
  cat >"/etc/systemd/system/$PROJECT-$1.service" <<EOF
[Unit]
Description=GotATicket $3
After=network-online.target mysql.service mariadb.service redis-server.service
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
# The app reads $ENV_FILE from its working directory.
WorkingDirectory=$APP_DIR
ExecStart=$APP_DIR/current/$PROJECT $2
Restart=always
RestartSec=3
TimeoutStopSec=30
LimitNOFILE=65536

NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ReadWritePaths=$DATA_DIR

[Install]
WantedBy=multi-user.target
EOF
}

activate_release() {
  step "Migrating the database and switching to the new release"
  GOTATICKET_BIN=$RELEASE/$PROJECT /usr/local/bin/$PROJECT migrate ||
    die "the database migration failed; the site keeps running the previous release"

  ln -sfn "$RELEASE" "$APP_DIR/current.new"
  mv -Tf "$APP_DIR/current.new" "$APP_DIR/current"
  systemctl restart "$PROJECT-web" "$PROJECT-worker"

  if $DEMO && [[ "$(get_env DEMO_DATA)" != true ]]; then
    info "Loading demo data"
    /usr/local/bin/$PROJECT seed
    set_env DEMO_DATA true # so a later move to production can refuse to put it live
  fi

  # Keep the newest releases for a quick roll back; delete older ones.
  find "$APP_DIR/releases" -mindepth 1 -maxdepth 1 -type d | sort -r |
    tail -n +$((KEEP_RELEASES + 1)) | xargs -r rm -rf
}

wait_until_healthy() {
  step "Waiting for the app to become healthy"
  local url i
  url="http://127.0.0.1:$APP_PORT/api/v1/health"
  for ((i = 0; i < 60; i++)); do
    if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
      info "Healthy: $url"
      return
    fi
    sleep 2
  done
  journalctl -u "$PROJECT-web" -n 25 --no-pager >&2 || true
  die "the app did not become healthy within 2 minutes; the log above shows why"
}

# ---- nginx --------------------------------------------------------------------------------

# write_nginx_site HTTPS(true|false)
write_nginx_site() {
  local https=$1 cert=/etc/letsencrypt/live/$DOMAIN
  {
    cat <<EOF
# Managed by GotATicket deploy-ubuntu.sh; changes are overwritten on the next deploy.
upstream ${PROJECT}_app {
    server 127.0.0.1:$APP_PORT;
    keepalive 16;
}

EOF
    if ! has_domain; then
      cat <<EOF
server {
    listen $PORT default_server;
$(listen6 "$PORT default_server")
    server_name _;
$(proxy_block)
}
EOF
    elif ! $https; then
      cat <<EOF
server {
    listen 80;
$(listen6 80)
    server_name $(site_names);
$(proxy_block)
}
EOF
    else
      cat <<EOF
server {
    listen 80;
$(listen6 80)
    server_name $(site_names);
    return 301 https://$DOMAIN\$request_uri;
}
EOF
      if $WWW; then
        cat <<EOF

server {
    listen 443 ssl;
$(listen6 "443 ssl")
    server_name www.$DOMAIN;
    ssl_certificate     $cert/fullchain.pem;
    ssl_certificate_key $cert/privkey.pem;
    return 301 https://$DOMAIN\$request_uri;
}
EOF
      fi
      cat <<EOF

server {
    listen 443 ssl;
$(listen6 "443 ssl")
    http2 on;
    server_name $DOMAIN;

    ssl_certificate     $cert/fullchain.pem;
    ssl_certificate_key $cert/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_session_cache   shared:${PROJECT}_ssl:10m;
    add_header Strict-Transport-Security "max-age=31536000" always;
$(proxy_block)
}
EOF
    fi
  } >"$NGINX_SITE"
}

# The names nginx answers to: the domain, plus www when its DNS points here too.
site_names() {
  if $WWW; then
    echo "$DOMAIN www.$DOMAIN"
  else
    echo "$DOMAIN"
  fi
}

# listen6 "PORT [FLAGS]": an IPv6 listen line, or nothing on a server without IPv6 (nginx
# refuses to start when it can't open an IPv6 socket).
listen6() {
  if [[ -s /proc/net/if_inet6 ]]; then
    printf '    listen [::]:%s;' "$1"
  fi
}

proxy_block() {
  cat <<'EOF'

    # Event images can be up to 5 MB.
    client_max_body_size 8m;
    server_tokens off;

    location / {
        proxy_pass http://gotaticket_app;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        # Replace, never append: the app trusts this header for rate limits and audit logs.
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
    }
EOF
}

# nginx versions before 1.25.1 don't know "http2 on;"; they take it on the listen line.
fix_http2_syntax() {
  local version
  version=$(nginx -v 2>&1 | grep -o '[0-9][0-9.]*' | head -1)
  if ! version_at_least "$version" 1.25.1; then
    sed -i -e '/^    http2 on;$/d' -e 's/listen \(\[::\]:\)\{0,1\}443 ssl;/listen \1443 ssl http2;/' "$NGINX_SITE"
  fi
}

enable_nginx_site() {
  ln -sfn "$NGINX_SITE" "/etc/nginx/sites-enabled/$PROJECT"
  if ! has_domain; then
    # Our site answers for the server's IP address, so Ubuntu's stock welcome page has to
    # step aside; any other site you have stays as it is.
    local stock=/etc/nginx/sites-enabled/default
    if [[ -L $stock && "$(readlink -f "$stock")" == /etc/nginx/sites-available/default ]]; then
      rm -f "$stock"
      info "Disabled nginx's default welcome page"
    elif grep -rlsE "listen[^;]*\\b$PORT\\b[^;]*default_server" /etc/nginx/sites-enabled /etc/nginx/conf.d |
      grep -v "/$PROJECT\$" | grep -q .; then
      die "another nginx site is already the default for port $PORT. Choose another port with --port."
    fi
  fi
  nginx -t 2>/dev/null || { nginx -t; die "nginx rejected the configuration in $NGINX_SITE"; }
  systemctl reload nginx
}

# resolves NAME: whether NAME's DNS points at this server.
resolves_here() {
  [[ -n $SERVER_IP ]] && getent ahostsv4 "$1" | awk '{print $1}' | grep -qx "$SERVER_IP"
}

# Gets (or extends) the Let's Encrypt certificate before anything else changes, so a
# domain whose DNS isn't ready yet stops the run with the site exactly as it was.
obtain_certificate() {
  step "Getting an HTTPS certificate for $DOMAIN"
  SERVER_IP=$(curl -4fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  valid_ipv4 "$SERVER_IP" || SERVER_IP=""

  if [[ -n $SERVER_IP ]] && ! resolves_here "$DOMAIN"; then
    local now
    now=$(getent ahostsv4 "$DOMAIN" | awk '{print $1}' | sort -u | tr '\n' ' ')
    die "$DOMAIN points to ${now:-nowhere yet}, not to this server ($SERVER_IP).
       At your domain registrar (GoDaddy: My Products > DNS), set the A record for @ to
       $SERVER_IP, delete any other A records for @ (such as \"Parked\"), and switch off
       domain forwarding. Once 'nslookup $DOMAIN' shows $SERVER_IP, run this script again.
       Nothing on this server has been changed."
  fi
  if resolves_here "www.$DOMAIN"; then
    WWW=true
  else
    info "www.$DOMAIN doesn't point here, so only $DOMAIN gets HTTPS (point www at $SERVER_IP and run again to add it)"
  fi

  local cert=/etc/letsencrypt/live/$DOMAIN/fullchain.pem
  if [[ -f $cert ]] && { ! $WWW || openssl x509 -in "$cert" -noout -ext subjectAltName 2>/dev/null | grep -q "DNS:www.$DOMAIN"; }; then
    info "Certificate already in place ($(openssl x509 -in "$cert" -noout -enddate | cut -d= -f2))"
    HTTPS_READY=true
    return
  fi

  # Let's Encrypt checks the domain over plain HTTP, so nginx must answer on port 80 first
  # (the firewall already allows it).
  write_nginx_site false
  enable_nginx_site
  local names=(-d "$DOMAIN")
  $WWW && names+=(-d "www.$DOMAIN")
  if ! certbot certonly --nginx --non-interactive --agree-tos --email "$EMAIL" \
    --cert-name "$DOMAIN" --expand "${names[@]}" --deploy-hook 'systemctl reload nginx' >/dev/null 2>&1; then
    tail -n 15 /var/log/letsencrypt/letsencrypt.log >&2 || true
    die "Let's Encrypt couldn't issue the certificate; the log above shows why. The most common
       causes are port 80 being closed in your hosting provider's firewall, or DNS that
       changed only minutes ago (wait, then run this script again)."
  fi
  info "Certificate issued for $(site_names); it renews automatically"
  HTTPS_READY=true
}

configure_nginx() {
  step "Configuring nginx"
  if has_domain; then
    write_nginx_site true
    fix_http2_syntax
  else
    write_nginx_site false
  fi
  enable_nginx_site
  info "nginx serves $(get_env PUBLIC_URL)$($WWW && echo " (www.$DOMAIN redirects there)") from 127.0.0.1:$APP_PORT"
}

# ---- Backups ------------------------------------------------------------------------------

install_backups() {
  step "Setting up daily backups ($BACKUP_DIR, kept for 14 days)"
  cat >/usr/local/sbin/$PROJECT-backup <<EOF
#!/usr/bin/env bash
# Backs up the GotATicket database, uploaded images and settings (.env holds the secrets
# that keep issued tickets valid). Installed by deploy-ubuntu.sh.
# Restore the database:
#   gunzip -c db-<time>.sql.gz | mysql --defaults-extra-file=$DB_CNF $DB_NAME
set -Eeuo pipefail
umask 077
dest=$BACKUP_DIR
keep_days=\${KEEP_DAYS:-14}
stamp=\$(date +%Y%m%d-%H%M%S)
mkdir -p "\$dest"

mysqldump --defaults-extra-file=$DB_CNF --single-transaction --quick --no-tablespaces \\
  $DB_NAME | gzip >"\$dest/db-\$stamp.sql.gz.part"
mv "\$dest/db-\$stamp.sql.gz.part" "\$dest/db-\$stamp.sql.gz"

tar czf "\$dest/files-\$stamp.tar.gz" -C / "${DATA_DIR#/}/uploads" "${ENV_FILE#/}"

find "\$dest" -maxdepth 1 -name '*.gz*' -mtime +"\$keep_days" -delete
echo "\$(date -Is) backup \$stamp done"
EOF
  chmod 700 /usr/local/sbin/$PROJECT-backup
  echo "30 2 * * * root /usr/local/sbin/$PROJECT-backup >>/var/log/$PROJECT-backup.log 2>&1" \
    >/etc/cron.d/$PROJECT-backup
  chmod 644 /etc/cron.d/$PROJECT-backup
  systemctl enable --now cron >/dev/null 2>&1
  info "Runs daily at 02:30. Copy $BACKUP_DIR somewhere else too (another server or cloud storage)."
}

# ---- Summary ------------------------------------------------------------------------------

summary() {
  local site admin_file=/root/$PROJECT-admin.txt status mode mail
  site=$(get_env PUBLIC_URL)
  # Kept up to date while the bootstrap password is still in .env.
  if [[ -n "$(get_env BOOTSTRAP_ADMIN_PASSWORD)" ]]; then
    (
      umask 077
      printf 'GotATicket first super administrator\nSign in: %s/sign-in\nEmail: %s\nPassword: %s\n' \
        "$site" "$(get_env BOOTSTRAP_ADMIN_EMAIL)" "$(get_env BOOTSTRAP_ADMIN_PASSWORD)" >"$admin_file"
    )
  fi

  if curl -fsS --max-time 10 "$site/api/v1/health" >/dev/null 2>&1; then
    status="${GREEN}reachable${RESET}"
  elif has_domain && ! $HTTPS_READY; then
    status="${YELLOW}no HTTPS certificate yet; run this script again once DNS points here${RESET}"
  else
    status="${YELLOW}running; if it doesn't open in your browser, allow port $(has_domain && echo 443 || echo "$PORT") in your hosting provider's firewall${RESET}"
  fi

  if ! $STAGING; then
    mode="production (Ozow)"
  else
    mode="test site (sandbox payments, no real money)"
  fi

  if [[ -z "$(get_env SMTP_HOST)" ]]; then
    mail="saved as files in $DATA_DIR/mail (not sent until SMTP is set in .env)"
  else
    mail="sent through $(get_env SMTP_HOST)"
  fi

  cat <<EOF

${GREEN}${BOLD}GotATicket is running.${RESET}

  Site:          $site   ($status)
  Mode:          $mode
  Release:       $(readlink "$APP_DIR/current")
  Settings:      $ENV_FILE
  Database:      $DB_NAME at ${DB_SOCKET:-$DB_HOST:$DB_PORT} (user $DB_USER)
  Admin sign-in: saved in $admin_file (readable by root only).
                 Change the password after signing in, then remove BOOTSTRAP_ADMIN_PASSWORD
                 from .env.
  Emails:        $mail
EOF

  if has_domain; then
    cat <<EOF

  Ozow webhook URL:       $site/api/v1/payments/webhooks/ozow
  Google redirect URI:    $site/api/v1/auth/google/callback
EOF
  else
    cat <<EOF

  ${YELLOW}Without a domain the site runs on plain HTTP, so use it for testing only:${RESET}
    - Passwords travel unencrypted; don't use ones you use elsewhere.
    - At the door, type ticket codes: browsers allow the camera only on HTTPS.
    - Installing the app, offline tickets and "Use my location" need HTTPS.
    - Google sign-in and live Ozow payments need a domain.
  With a domain pointing at $HOST_IP, run this script again with
    --domain <your domain> --email <your email> --ozow
EOF
  fi

  cat <<EOF

  Useful commands:
    systemctl status $PROJECT-web $PROJECT-worker
    journalctl -u $PROJECT-web -u $PROJECT-worker -f        # live logs
    sudo systemctl restart $PROJECT-web $PROJECT-worker     # after editing .env
    sudo /usr/local/sbin/$PROJECT-backup                    # back up now
  New version: unzip it, cd into it and run  sudo ./deploy.sh
EOF
}

main() {
  parse_args "$@"
  check_system
  ensure_swap
  install_packages
  install_go
  install_node
  has_domain || detect_ip
  create_service_user
  fetch_source
  resolve_ports
  check_ports
  configure_firewall
  if has_domain; then
    obtain_certificate
  fi
  write_env
  check_database
  check_redis
  build_release
  install_cli
  install_services
  activate_release
  wait_until_healthy
  configure_nginx
  install_backups
  summary
}

main "$@"
