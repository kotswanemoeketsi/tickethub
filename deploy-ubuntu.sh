#!/usr/bin/env bash
#
# Deploys GotATicket on one Ubuntu server (22.04 or 24.04, amd64 or arm64) without Docker:
# MySQL and Redis from Ubuntu's packages, the app built from source and run by systemd.
#
# Either on the server's IP address and a port (no domain yet, plain HTTP), or on a domain
# with HTTPS through Caddy and Let's Encrypt.
#
# What it does, safely re-runnable:
#   1. Installs MySQL, Redis, Go and Node.js (to build), and Caddy when you give a domain
#   2. Adds swap on small servers so the build doesn't run out of memory
#   3. Opens only SSH and the site's port(s) in the firewall
#   4. Creates the database, a system user and /opt/gotaticket/.env with random secrets
#      (an existing .env is kept; only values you pass are changed)
#   5. Builds a new release, migrates the database, switches to it and restarts the
#      gotaticket-web and gotaticket-worker services, then waits until the site is healthy
#   6. Sets up daily backups of the database, uploaded images and settings
#
# Usage (as root, from the unzipped project folder or with --repo):
#
#   # No domain yet: http://<server ip>:8000, sandbox payments
#   sudo ./scripts/deploy-ubuntu.sh --port 8000 --email you@example.co.za
#
#   # Later, with a domain pointing at the server: HTTPS and live Paystack payments
#   sudo ./scripts/deploy-ubuntu.sh --domain tickets.example.co.za --email you@example.co.za \
#        --paystack-key sk_live_xxx
#
# Moving from the IP address to a domain keeps all data: run the script again with --domain.
# To release a new version, upload the new code (or use --repo) and run the script again.

set -Eeuo pipefail

readonly PROJECT=gotaticket
readonly SERVICE_USER=$PROJECT
readonly APP_DIR=/opt/$PROJECT
readonly ENV_FILE=$APP_DIR/.env
readonly DATA_DIR=/var/lib/$PROJECT
readonly BACKUP_DIR=/var/backups/$PROJECT
readonly MIN_GO=1.24.0
readonly MIN_NODE=20.19.0
readonly NODE_MAJOR=22
readonly KEEP_RELEASES=3
export PATH=/usr/local/go/bin:/usr/local/bin:$PATH

DOMAIN=""
HOST_IP=""
PORT=""
EMAIL=""
ADMIN_EMAIL=""
PAYSTACK_KEY=""
REPO=""
BRANCH=main
STAGING=false
DEMO=false
FIREWALL=true
RELEASE=""

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
Deploys GotATicket on this Ubuntu server, without Docker.

Usage: sudo $0 [--ip IP] [--port PORT] [options]        no domain: http://IP:PORT
       sudo $0 --domain DOMAIN --email EMAIL [options]  with a domain: https://DOMAIN

Without a domain (plain HTTP, for testing and demos):
  --ip IP                The server's public IPv4 address (default: detected)
  --port PORT            Port the site listens on (default: 8000)
                         Always a test site: sandbox payments, or a Paystack test key.

With a domain (HTTPS through Caddy and Let's Encrypt):
  --domain DOMAIN        e.g. tickets.example.co.za; its DNS A record must point here
  --email EMAIL          Required; Let's Encrypt sends certificate notices here
  --port PORT            Internal port Caddy forwards to (default: 8000)

Payments:
  --paystack-key KEY     Paystack secret key. sk_live_ keys need a domain (production);
                         sk_test_ keys work on a test site
  --staging              Domain only: a test site with sandbox payments, no real money

Options:
  --email EMAIL          Your email; also the first super administrator's sign-in
  --admin-email EMAIL    First super administrator (default: --email, else
                         admin@$PROJECT.local)
  --demo                 Load demo events, orders and accounts (test sites only)
  --repo URL             Clone or update the code from git instead of using this folder.
                         For a private repository use a deploy key, or
                         https://<token>@github.com/<owner>/$PROJECT.git
  --branch NAME          Git branch to deploy (default: main)
  --no-firewall          Leave the firewall (ufw) as it is
  -h, --help             Show this help
EOF
}

# ---- Arguments ----------------------------------------------------------------------------

parse_args() {
  while (($#)); do
    case "$1" in
      --domain) DOMAIN="${2:?--domain needs a value}"; shift 2 ;;
      --ip) HOST_IP="${2:?--ip needs a value}"; shift 2 ;;
      --port) PORT="${2:?--port needs a value}"; shift 2 ;;
      --email) EMAIL="${2:?--email needs a value}"; shift 2 ;;
      --admin-email) ADMIN_EMAIL="${2:?--admin-email needs a value}"; shift 2 ;;
      --paystack-key) PAYSTACK_KEY="${2:?--paystack-key needs a value}"; shift 2 ;;
      --repo) REPO="${2:?--repo needs a value}"; shift 2 ;;
      --branch) BRANCH="${2:?--branch needs a value}"; shift 2 ;;
      --staging) STAGING=true; shift ;;
      --demo) DEMO=true; shift ;;
      --no-firewall) FIREWALL=false; shift ;;
      -h | --help) usage; exit 0 ;;
      *) usage >&2; die "unknown option: $1" ;;
    esac
  done

  if [[ -n $PORT ]]; then
    if [[ ! $PORT =~ ^[0-9]{1,5}$ ]] || ((10#$PORT < 1 || 10#$PORT > 65535)); then
      die "--port must be a number from 1 to 65535"
    fi
    PORT=$((10#$PORT))
  fi
  if [[ -n $EMAIL ]]; then
    [[ $EMAIL =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "--email is not a valid email address"
  fi

  if [[ -n $DOMAIN ]]; then
    [[ -z $HOST_IP ]] || die "use either --domain or --ip, not both"
    [[ $DOMAIN =~ ^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$ ]] ||
      die "--domain must be a domain name such as tickets.example.co.za (no https:// or path)"
    [[ -n $EMAIL ]] || { usage >&2; die "--email is required with --domain"; }
    if $STAGING && [[ -n $PAYSTACK_KEY ]]; then
      die "use either --staging or --paystack-key, not both"
    fi
    if $DEMO && ! $STAGING; then
      die "--demo needs --staging on a domain; demo data must never reach a live site"
    fi
  else
    # No HTTPS without a domain: passwords would travel unencrypted, so this is always a
    # test site and live keys are refused.
    STAGING=true
    if [[ $PAYSTACK_KEY == sk_live_* ]]; then
      die "live Paystack keys need a domain with HTTPS; use an sk_test_ key or --domain"
    fi
    if [[ -n $HOST_IP ]]; then
      valid_ipv4 "$HOST_IP" || die "--ip must be an IPv4 address such as 102.165.10.20"
    fi
  fi
  ADMIN_EMAIL="${ADMIN_EMAIL:-${EMAIL:-admin@$PROJECT.local}}"
}

valid_ipv4() {
  local octet re='^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$'
  [[ $1 =~ $re ]] || return 1
  for octet in ${1//./ }; do
    ((10#$octet <= 255)) || return 1
  done
}

has_domain() { [[ -n $DOMAIN ]]; }

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
    22.04 | 24.04) ;;
    *) warn "tested on Ubuntu 22.04 and 24.04; found ${VERSION_ID:-unknown}, continuing anyway" ;;
  esac
  case "$(dpkg --print-architecture)" in
    amd64 | arm64) ;;
    *) die "only amd64 and arm64 servers are supported" ;;
  esac
  info "Ubuntu ${VERSION_ID:-?} ($(dpkg --print-architecture)), $(nproc) CPU, $(free -h | awk '/^Mem:/ {print $2}') RAM"

  local free_gb
  free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  ((free_gb >= 10)) || warn "only ${free_gb} GB free on /; 10 GB or more is recommended"
}

mem_mb() { awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo; }

ensure_swap() {
  local swap_mb
  swap_mb=$(awk '/^SwapTotal:/ {print int($2 / 1024)}' /proc/meminfo)
  if (($(mem_mb) >= 3500 || swap_mb > 0)); then
    return
  fi
  step "Adding a 2 GB swap file (this server has $(mem_mb) MB RAM)"
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
  step "Installing MySQL, Redis and base packages"
  apt-get update -qq
  apt_install ca-certificates curl gnupg git rsync openssl ufw cron jq xz-utils tar \
    mysql-server redis-server
  systemctl enable --now mysql redis-server >/dev/null
  info "$(mysql --version | awk '{print "MySQL", $3}'), $(redis-server --version | awk '{print "Redis", $3}' | tr -d 'v=')"
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

install_caddy() {
  if command -v caddy >/dev/null; then
    return
  fi
  step "Installing Caddy (HTTPS)"
  curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/gpg.key |
    gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
    -o /etc/apt/sources.list.d/caddy-stable.list
  chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -qq
  apt_install caddy
}

# ---- Address and firewall -----------------------------------------------------------------

# The address people type in their browser: the public IP, or the first local one on a
# server without a public address (for example inside an office network).
detect_ip() {
  [[ -n $HOST_IP ]] && return
  HOST_IP=$(curl -4fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  valid_ipv4 "$HOST_IP" || HOST_IP=$(hostname -I | awk '{print $1}')
  valid_ipv4 "$HOST_IP" || die "could not work out this server's IP address; pass it with --ip"
  info "Using IP address $HOST_IP (pass --ip to choose another)"
}

# --port, else the port from an earlier run, else 8000.
resolve_port() {
  if [[ -z $PORT && -f $ENV_FILE ]]; then
    local addr
    addr=$(get_env HTTP_ADDR)
    [[ ${addr##*:} =~ ^[0-9]+$ ]] && PORT=${addr##*:}
  fi
  PORT=${PORT:-8000}
}

configure_firewall() {
  if ! $FIREWALL; then
    info "Skipping the firewall (--no-firewall)"
    return
  fi
  step "Configuring the firewall"
  # Allow every port sshd listens on before enabling ufw, so this session isn't cut off.
  local ports port site_ports
  ports=$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2}' || true)
  for port in ${ports:-22}; do
    ufw allow "$port/tcp" comment 'SSH' >/dev/null
  done
  if has_domain; then
    ufw allow 80/tcp comment 'HTTP' >/dev/null
    ufw allow 443/tcp comment 'HTTPS' >/dev/null
    ufw allow 443/udp comment 'HTTP/3' >/dev/null
    site_ports="80, 443"
  else
    ufw allow "$PORT/tcp" comment 'GotATicket' >/dev/null
    site_ports="$PORT"
  fi
  ufw --force enable >/dev/null
  info "Open: SSH (${ports:-22}), $site_ports. MySQL and Redis listen on this server only."
  info "If your cloud provider has its own firewall or security group, open $site_ports there too."
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

  local src
  src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  [[ -f $src/backend/go.mod && -f $src/frontend/package.json ]] ||
    die "run this script from the unzipped GotATicket folder, or pass --repo"
  if [[ $src != "$src_dir" ]]; then
    rsync -a --delete \
      --exclude '.env' --exclude '.git/' --exclude 'node_modules/' \
      --exclude 'frontend/dist/' --exclude 'backend/storage/' --exclude 'backend/bin/' \
      "$src/" "$src_dir/"
  fi
  info "Copied from $src"
}

# ---- Configuration ------------------------------------------------------------------------

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

get_env() {
  awk -v key="$1" 'index($0, key "=") == 1 { print substr($0, length(key) + 2); exit }' "$ENV_FILE"
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

write_env_template() {
  cat >"$ENV_FILE" <<EOF
# GotATicket settings, read by the gotaticket-web and gotaticket-worker services.
# After a change, apply it with: sudo systemctl restart gotaticket-web gotaticket-worker
# Keep comments on their own lines. Never change QR_SIGNING_KEY: every ticket already
# issued would stop scanning.

APP_NAME=GotATicket
APP_ENV=staging
HTTP_ADDR=:8000
PUBLIC_URL=http://localhost:8000
API_URL=http://localhost:8000
LOG_FORMAT=json

DATABASE_DSN=
REDIS_URL=redis://127.0.0.1:6379/0
AUTO_MIGRATE=false
EMBEDDED_WORKER=false

JWT_SECRET=
QR_SIGNING_KEY=
COOKIE_SECURE=false
TRUST_PROXY_HEADERS=false
PASSWORD_HASH_ITERATIONS=600000

# First super administrator, created on first start. Remove the password once you have
# signed in and changed it.
BOOTSTRAP_ADMIN_EMAIL=
BOOTSTRAP_ADMIN_PASSWORD=

# sandbox = built-in test gateway; paystack = real payments (required in production).
PAYMENT_PROVIDER=sandbox
PAYSTACK_SECRET_KEY=
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

write_env() {
  step "Writing the configuration ($ENV_FILE)"
  local first_run=false
  if [[ ! -f $ENV_FILE ]]; then
    write_env_template
    first_run=true
  fi
  secure_env_file

  # Checked before anything changes, so a refused run leaves the settings as they were.
  if has_domain && ! $STAGING && [[ -n $PAYSTACK_KEY ]]; then
    refuse_demo_data_in_production
  fi

  # Secrets are generated once and never replaced: a new QR_SIGNING_KEY would invalidate
  # every ticket already issued.
  local key
  for key in JWT_SECRET QR_SIGNING_KEY SANDBOX_WEBHOOK_SECRET; do
    [[ -n "$(get_env "$key")" ]] || set_env "$key" "$(random_secret)"
  done
  [[ -n "$(get_env DATABASE_DSN)" ]] ||
    set_env DATABASE_DSN "$PROJECT:$(random_password)@tcp(127.0.0.1:3306)/$PROJECT"
  if $first_run; then
    set_env BOOTSTRAP_ADMIN_EMAIL "$ADMIN_EMAIL"
    set_env BOOTSTRAP_ADMIN_PASSWORD "$(random_password)"
  fi

  local site
  if has_domain; then
    site="https://$DOMAIN"
    set_env HTTP_ADDR "127.0.0.1:$PORT" # only Caddy reaches the app
    set_env COOKIE_SECURE true
    set_env TRUST_PROXY_HEADERS true # client IPs come from Caddy's X-Forwarded-For
    if [[ "$(get_env MAIL_FROM)" == *"@$PROJECT.local>" ]]; then
      set_env MAIL_FROM "GotATicket <no-reply@$DOMAIN>"
    fi
  else
    site="http://$HOST_IP:$PORT"
    set_env HTTP_ADDR ":$PORT" # browsers connect straight to the app
    set_env COOKIE_SECURE false # browsers drop secure cookies on plain HTTP
    set_env TRUST_PROXY_HEADERS false # no proxy in front, so the header could be forged
  fi
  set_env PUBLIC_URL "$site"
  set_env API_URL "$site"

  if ! has_domain; then
    set_env APP_ENV staging
    if [[ -n $PAYSTACK_KEY ]]; then
      set_env PAYMENT_PROVIDER paystack
      set_env PAYSTACK_SECRET_KEY "$PAYSTACK_KEY"
    elif [[ "$(get_env PAYSTACK_SECRET_KEY)" == sk_live_* || -z "$(get_env PAYSTACK_SECRET_KEY)" ]]; then
      set_env PAYMENT_PROVIDER sandbox
    fi
  elif $STAGING; then
    set_env APP_ENV staging
    set_env PAYMENT_PROVIDER sandbox
  elif [[ -n $PAYSTACK_KEY ]]; then
    set_env APP_ENV production
    set_env PAYMENT_PROVIDER paystack
    set_env PAYSTACK_SECRET_KEY "$PAYSTACK_KEY"
  elif [[ "$(get_env APP_ENV)" == production && -n "$(get_env PAYSTACK_SECRET_KEY)" ]]; then
    : # a production site from an earlier run; keep its settings
  elif [[ "$(get_env APP_ENV)" == staging ]]; then
    STAGING=true
  else
    die "choose --paystack-key KEY for a live site or --staging for a test site"
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
  die "this server holds demo events, orders and accounts, which must not go live. To start
       with an empty database (this deletes all data and uploaded images), run:
         sudo systemctl stop $PROJECT-web $PROJECT-worker
         sudo mysql -e 'DROP DATABASE $PROJECT'
         sudo find $DATA_DIR/uploads $DATA_DIR/mail -mindepth 1 -delete
         sudo sed -i '/^DEMO_DATA=/d' $ENV_FILE
       then run this script again. The first super administrator is created again from
       BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD in $ENV_FILE."
}

# ---- Database -----------------------------------------------------------------------------

configure_mysql() {
  step "Setting up the MySQL database"
  # Sized once for this server; edit the file by hand afterwards if you like.
  local cnf=/etc/mysql/mysql.conf.d/$PROJECT.cnf
  if [[ ! -f $cnf ]]; then
    local mem pool_mb perf=ON
    mem=$(mem_mb)
    pool_mb=$((mem / 4))
    ((pool_mb < 256)) && pool_mb=256
    ((pool_mb > 4096)) && pool_mb=4096
    ((mem < 4000)) && perf=OFF # saves about 400 MB on small servers
    cat >"$cnf" <<EOF
# Written by GotATicket deploy-ubuntu.sh for a server with $mem MB RAM.
[mysqld]
bind-address = 127.0.0.1
innodb_buffer_pool_size = ${pool_mb}M
performance_schema = $perf
EOF
    systemctl restart mysql
    info "Buffer pool ${pool_mb} MB, performance schema $perf ($cnf)"
  fi

  # Ubuntu's MySQL lets root in through the local socket, so no root password is needed.
  local dsn password
  dsn=$(get_env DATABASE_DSN)
  password=$(sed -E 's/^[^:]+:([^@]*)@.*/\1/' <<<"$dsn")
  [[ $password =~ ^[a-f0-9]{32}$ ]] || die "DATABASE_DSN in $ENV_FILE has an unexpected format"
  mysql --protocol=socket -uroot <<SQL || die "could not sign in to MySQL as root through its socket (sudo mysql); if root has a password, add it to /root/.my.cnf and run again"
CREATE DATABASE IF NOT EXISTS $PROJECT CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER IF NOT EXISTS '$PROJECT'@'localhost' IDENTIFIED BY '$password';
CREATE USER IF NOT EXISTS '$PROJECT'@'127.0.0.1' IDENTIFIED BY '$password';
ALTER USER '$PROJECT'@'localhost' IDENTIFIED BY '$password';
ALTER USER '$PROJECT'@'127.0.0.1' IDENTIFIED BY '$password';
GRANT ALL PRIVILEGES ON $PROJECT.* TO '$PROJECT'@'localhost', '$PROJECT'@'127.0.0.1';
SQL
  info "Database $PROJECT, user $PROJECT (password in DATABASE_DSN)"

  # Redis holds only disposable data (rate limits, caches): cap it and never write to disk.
  redis-cli CONFIG SET maxmemory 128mb >/dev/null
  redis-cli CONFIG SET maxmemory-policy allkeys-lru >/dev/null
  redis-cli CONFIG SET save "" >/dev/null
  redis-cli CONFIG REWRITE >/dev/null || warn "could not save the Redis settings to redis.conf"
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
  write_unit web serve "website, portal and API" \
    "# Lets --port be below 1024 without running as root." \
    "AmbientCapabilities=CAP_NET_BIND_SERVICE"
  write_unit worker worker "background jobs: emails, reservation expiry, payment checks"
  systemctl daemon-reload
  systemctl enable "$PROJECT-web" "$PROJECT-worker" >/dev/null 2>&1
}

# write_unit ROLE COMMAND DESCRIPTION [EXTRA SERVICE LINES...]
write_unit() {
  local role=$1 command=$2 description=$3
  shift 3
  local extra=""
  (($#)) && extra=$(printf '%s\n' "$@")
  cat >"/etc/systemd/system/$PROJECT-$role.service" <<EOF
[Unit]
Description=GotATicket $description
After=network-online.target mysql.service redis-server.service
Wants=network-online.target mysql.service redis-server.service

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
# The app reads $ENV_FILE from its working directory.
WorkingDirectory=$APP_DIR
ExecStart=$APP_DIR/current/$PROJECT $command
Restart=always
RestartSec=3
TimeoutStopSec=30
LimitNOFILE=65536
$extra
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
  step "Waiting for the site to become healthy"
  local url i
  url="http://127.0.0.1:$PORT/api/v1/health"
  for ((i = 0; i < 60; i++)); do
    if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
      info "Healthy: $url"
      return
    fi
    sleep 2
  done
  systemctl --no-pager status "$PROJECT-web" >&2 || true
  journalctl -u "$PROJECT-web" -n 40 --no-pager >&2 || true
  die "the site did not become healthy within 2 minutes; the logs above show why"
}

# ---- HTTPS --------------------------------------------------------------------------------

configure_caddy() {
  step "Configuring HTTPS for $DOMAIN"
  local caddyfile=/etc/caddy/Caddyfile marker="# Managed by GotATicket deploy-ubuntu.sh"
  if [[ -f $caddyfile ]] && ! grep -qF "$marker" "$caddyfile"; then
    cp "$caddyfile" "$caddyfile.bak.$(date +%Y%m%d%H%M%S)"
    info "Saved the previous Caddyfile next to it as a .bak file"
  fi

  cat >"$caddyfile" <<EOF
$marker
# Edits are overwritten on the next deploy; put other sites in /etc/caddy/sites/*.caddy.
{
	email $EMAIL
}

$DOMAIN {
	encode zstd gzip
	reverse_proxy 127.0.0.1:$PORT
	header -Server
}

import /etc/caddy/sites/*.caddy
EOF
  mkdir -p /etc/caddy/sites
  caddy validate --config "$caddyfile" --adapter caddyfile >/dev/null 2>&1 ||
    die "the Caddy configuration is invalid; check with: caddy validate --config $caddyfile"
  systemctl enable caddy >/dev/null
  systemctl reload-or-restart caddy

  check_dns
}

check_dns() {
  local server_ip domain_ips
  server_ip=$(curl -4fsS --max-time 5 https://api.ipify.org || true)
  domain_ips=$(getent ahostsv4 "$DOMAIN" | awk '{print $1}' | sort -u | tr '\n' ' ' || true)
  if [[ -z $domain_ips ]]; then
    warn "$DOMAIN does not resolve yet. Add a DNS A record pointing to ${server_ip:-this server};" \
      "Caddy gets the certificate automatically once it does."
  elif [[ -n $server_ip && " $domain_ips" != *" $server_ip "* ]]; then
    warn "$DOMAIN points to ${domain_ips% } but this server is $server_ip. HTTPS starts working" \
      "once DNS points here (behind Cloudflare, use DNS-only mode until the certificate is issued)."
  fi
}

# ---- Backups ------------------------------------------------------------------------------

install_backups() {
  step "Setting up daily backups ($BACKUP_DIR, kept for 14 days)"
  cat >/usr/local/sbin/$PROJECT-backup <<EOF
#!/usr/bin/env bash
# Backs up the GotATicket database, uploaded images and settings (.env holds the secrets
# that keep issued tickets valid). Installed by deploy-ubuntu.sh.
# Restore the database: gunzip -c db-<time>.sql.gz | sudo mysql $PROJECT
set -Eeuo pipefail
umask 077
dest=$BACKUP_DIR
keep_days=\${KEEP_DAYS:-14}
stamp=\$(date +%Y%m%d-%H%M%S)
mkdir -p "\$dest"

mysqldump --protocol=socket -uroot --single-transaction --quick --routines --triggers \\
  --no-tablespaces $PROJECT | gzip >"\$dest/db-\$stamp.sql.gz.part"
mv "\$dest/db-\$stamp.sql.gz.part" "\$dest/db-\$stamp.sql.gz"

tar czf "\$dest/files-\$stamp.tar.gz" -C / "${DATA_DIR#/}/uploads" "${ENV_FILE#/}"

find "\$dest" -maxdepth 1 -name '*.gz*' -mtime +"\$keep_days" -delete
echo "\$(date -Is) backup \$stamp done"
EOF
  chmod 700 /usr/local/sbin/$PROJECT-backup
  echo "30 2 * * * root /usr/local/sbin/$PROJECT-backup >>/var/log/$PROJECT-backup.log 2>&1" \
    >/etc/cron.d/$PROJECT-backup
  chmod 644 /etc/cron.d/$PROJECT-backup
  systemctl enable --now cron >/dev/null
  info "Runs daily at 02:30. Copy $BACKUP_DIR to another place too (another server or cloud storage)."
}

# ---- Summary ------------------------------------------------------------------------------

summary() {
  local site admin_file=/root/$PROJECT-admin.txt status mode mail rerun
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
  elif has_domain; then
    status="${YELLOW}not yet over HTTPS; Caddy keeps trying. Check DNS, then: journalctl -u caddy -n 50${RESET}"
  else
    status="${YELLOW}running; if it doesn't open in your browser, allow port $PORT in your cloud provider's firewall${RESET}"
  fi

  if ! $STAGING; then
    mode="production (Paystack)"
  elif [[ "$(get_env PAYMENT_PROVIDER)" == paystack ]]; then
    mode="test site (Paystack test key)"
  else
    mode="test site (sandbox payments, no real money)"
  fi

  if [[ -z "$(get_env SMTP_HOST)" ]]; then
    mail="saved as files in $DATA_DIR/mail (not sent until SMTP is set in .env)"
  else
    mail="sent through $(get_env SMTP_HOST)"
  fi

  if has_domain; then
    rerun="--domain $DOMAIN --email $EMAIL"
  else
    rerun="--ip $HOST_IP --port $PORT"
  fi

  cat <<EOF

${GREEN}${BOLD}GotATicket is running.${RESET}

  Site:          $site   ($status)
  Mode:          $mode
  Release:       $(readlink "$APP_DIR/current")
  Settings:      $ENV_FILE
  Admin sign-in: saved in $admin_file (readable by root only).
                 Change the password after signing in, then remove BOOTSTRAP_ADMIN_PASSWORD
                 from .env.
  Emails:        $mail
EOF

  if has_domain; then
    cat <<EOF

  Paystack webhook URL:   $site/api/v1/payments/webhooks/paystack
  Google redirect URI:    $site/api/v1/auth/google/callback
EOF
  else
    cat <<EOF

  ${YELLOW}Without a domain the site runs on plain HTTP, so use it for testing only:${RESET}
    - Passwords travel unencrypted; don't use ones you use elsewhere.
    - At the door, type ticket codes: browsers allow the camera only on HTTPS.
    - Installing the app, offline tickets and "Use my location" need HTTPS.
    - Google sign-in and live Paystack payments need a domain.
  When you have a domain, point its DNS A record at $HOST_IP and run:
    sudo $APP_DIR/src/scripts/deploy-ubuntu.sh --domain <your domain> --email <your email> --paystack-key <sk_live_...>
  $([[ "$(get_env DEMO_DATA)" == true ]] && echo "This server has demo data; the script will ask you to clear it before going live." || echo "Your events, orders and accounts are kept.")
EOF
  fi

  cat <<EOF

  Useful commands:
    systemctl status $PROJECT-web $PROJECT-worker
    journalctl -u $PROJECT-web -u $PROJECT-worker -f        # live logs
    sudo systemctl restart $PROJECT-web $PROJECT-worker     # after editing .env
    sudo /usr/local/sbin/$PROJECT-backup                    # back up now
  New version: unzip it, cd into it and run  sudo ./scripts/deploy-ubuntu.sh $rerun
EOF
}

main() {
  parse_args "$@"
  check_system
  ensure_swap
  install_packages
  install_go
  install_node
  if has_domain; then
    install_caddy
  else
    detect_ip
  fi
  create_service_user
  fetch_source
  resolve_port
  configure_firewall
  write_env
  configure_mysql
  build_release
  install_cli
  install_services
  activate_release
  wait_until_healthy
  if has_domain; then
    configure_caddy
  fi
  install_backups
  summary
}

main "$@"