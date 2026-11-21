#!/usr/bin/env bash
#
# Deploys GotATicket on one Ubuntu server (22.04 or 24.04, amd64 or arm64), either on the
# server's IP address and a port (no domain yet, plain HTTP) or on a domain with HTTPS.
#
# What it does, safely re-runnable:
#   1. Installs Docker Engine and Docker Compose, plus Caddy for HTTPS when you give a domain
#   2. Adds swap on small servers so the image build doesn't run out of memory
#   3. Opens only SSH and the site's port(s) in the firewall
#   4. Copies or clones the code to /opt/gotaticket and writes a .env with random secrets
#      (an existing .env is kept; only values you pass are changed)
#   5. Builds and starts the platform, then waits until it is healthy
#   6. Sets up daily backups of the database and uploaded images
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
readonly BACKUP_DIR=/var/backups/$PROJECT
readonly MIN_COMPOSE=2.24.0

APP_DIR=/opt/$PROJECT
DOMAIN=""
HOST_IP=""
PORT=""
EMAIL="kotswane@gmail.com"
ADMIN_EMAIL="kotswane@gmail.com"
PAYSTACK_KEY=""
REPO=""
BRANCH=main
STAGING=false
DEMO=false
FIREWALL=true

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
Deploys GotATicket on this Ubuntu server.

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
                         admin@gotaticket.local)
  --demo                 Load demo events, orders and accounts (test sites only)
  --repo URL             Clone or update the code from git instead of using this folder.
                         For a private repository use a deploy key, or
                         https://<token>@github.com/<owner>/gotaticket.git
  --branch NAME          Git branch to deploy (default: main)
  --dir PATH             Where the app lives (default: /opt/gotaticket)
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
      --dir) APP_DIR="${2:?--dir needs a value}"; shift 2 ;;
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
    # No HTTPS without a domain: passwords and card redirects would travel unencrypted,
    # so this is always a test site and live keys are refused.
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

# The address people type in their browser: the public IP, or the first local one on a
# server without a public address (for example inside an office network).
detect_ip() {
  [[ -n $HOST_IP ]] && return
  HOST_IP=$(curl -4fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
  valid_ipv4 "$HOST_IP" || HOST_IP=$(hostname -I | awk '{print $1}')
  valid_ipv4 "$HOST_IP" || die "could not work out this server's IP address; pass it with --ip"
  info "Using IP address $HOST_IP (pass --ip to choose another)"
}

has_domain() { [[ -n $DOMAIN ]]; }

# --port, else the port from an earlier run's .env, else 8000.
resolve_port() {
  if [[ -z $PORT ]]; then
    [[ -f $APP_DIR/.env ]] && PORT=$(get_env WEB_PORT | grep -E '^[0-9]+$' || true)
    PORT=${PORT:-8000}
  fi
}

# ---- System checks ------------------------------------------------------------------------

check_system() {
  step "Checking the server"
  [[ $EUID -eq 0 ]] || die "run this script as root (sudo $0 ...)"
  [[ -r /etc/os-release ]] || die "cannot read /etc/os-release"
  # shellcheck source=/dev/null
  . /etc/os-release
  [[ ${ID:-} == ubuntu ]] || die "this script supports Ubuntu only (found ${ID:-unknown})"
  case "${VERSION_ID:-}" in
    22.04 | 24.04) ;;
    *) warn "tested on Ubuntu 22.04 and 24.04; found ${VERSION_ID:-unknown}, continuing anyway" ;;
  esac
  info "Ubuntu ${VERSION_ID:-?} ($(dpkg --print-architecture)), $(nproc) CPU, $(free -h | awk '/^Mem:/ {print $2}') RAM"

  local free_gb
  free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  ((free_gb >= 10)) || warn "only ${free_gb} GB free on /; 10 GB or more is recommended"
}

ensure_swap() {
  local mem_mb swap_mb
  mem_mb=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
  swap_mb=$(awk '/^SwapTotal:/ {print int($2 / 1024)}' /proc/meminfo)
  if ((mem_mb >= 3500 || swap_mb > 0)); then
    return
  fi
  step "Adding a 2 GB swap file (this server has ${mem_mb} MB RAM)"
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

install_base_packages() {
  step "Installing base packages"
  apt-get update -qq
  apt_install ca-certificates curl gnupg git rsync openssl ufw cron
}

version_at_least() { # version_at_least HAVE NEED
  [[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" == "$2" ]]
}

install_docker() {
  if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
    step "Installing Docker Engine and Docker Compose"
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
    local codename
    # shellcheck source=/dev/null
    codename=$(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $codename stable" \
      >/etc/apt/sources.list.d/docker.list
    apt-get update -qq
    apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  fi
  systemctl enable --now docker >/dev/null

  local have
  have=$(docker compose version --short | sed 's/^v//')
  if ! version_at_least "$have" "$MIN_COMPOSE"; then
    step "Updating Docker Compose ($have is older than $MIN_COMPOSE)"
    apt-get update -qq
    apt_install docker-compose-plugin
    have=$(docker compose version --short | sed 's/^v//')
    version_at_least "$have" "$MIN_COMPOSE" || die "Docker Compose $MIN_COMPOSE or later is needed (found $have)"
  fi
  info "$(docker --version), Compose $have"
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

# ---- Firewall -----------------------------------------------------------------------------

configure_firewall() {
  if ! $FIREWALL; then
    info "Skipping the firewall (--no-firewall)"
    return
  fi
  step "Configuring the firewall"
  # Allow every port sshd listens on before enabling ufw, so this session isn't cut off.
  local ports port
  ports=$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2}' || true)
  for port in ${ports:-22}; do
    ufw allow "$port/tcp" comment 'SSH' >/dev/null
  done
  local site_ports
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
  info "Open: SSH (${ports:-22}), $site_ports. MySQL, Redis and Mailpit are not reachable from outside."
  info "If your cloud provider has its own firewall or security group, open $site_ports there too."
}

# ---- Code ---------------------------------------------------------------------------------

fetch_source() {
  step "Getting the code into $APP_DIR"
  mkdir -p "$APP_DIR"

  if [[ -n $REPO ]]; then
    if [[ -d $APP_DIR/.git ]]; then
      git -C "$APP_DIR" remote set-url origin "$REPO"
      git -C "$APP_DIR" fetch --quiet origin "$BRANCH"
      git -C "$APP_DIR" checkout --quiet "$BRANCH"
      git -C "$APP_DIR" merge --quiet --ff-only "origin/$BRANCH"
    elif [[ -z "$(ls -A "$APP_DIR")" ]]; then
      git clone --quiet --branch "$BRANCH" "$REPO" "$APP_DIR"
    else
      die "$APP_DIR is not empty and not a git checkout; move its contents away or use --dir"
    fi
    info "Deployed $(git -C "$APP_DIR" log -1 --format='%h %s')"
    return
  fi

  local src
  src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  [[ -f $src/docker-compose.yml && -f $src/Dockerfile ]] ||
    die "run this script from the unzipped GotATicket folder, or pass --repo"
  if [[ $src != "$(cd "$APP_DIR" && pwd)" ]]; then
    # .env and the protected paths below are excluded, so --delete never removes them.
    rsync -a --delete \
      --exclude '.env' --exclude '.git/' --exclude 'node_modules/' \
      --exclude 'frontend/dist/' --exclude 'backend/storage/' \
      "$src/" "$APP_DIR/"
  fi
  info "Copied from $src"
}

# ---- Configuration ------------------------------------------------------------------------

# set_env KEY VALUE: replaces KEY=... in .env, or appends it. Handles any characters.
set_env() {
  local file="$APP_DIR/.env" tmp
  tmp=$(mktemp "$APP_DIR/.env.XXXXXX")
  KEY="$1" VALUE="$2" awk '
    BEGIN { key = ENVIRON["KEY"]; line = key "=" ENVIRON["VALUE"] }
    index($0, key "=") == 1 { if (!done) print line; done = 1; next }
    { print }
    END { if (!done) print line }
  ' "$file" >"$tmp"
  chmod 600 "$tmp"
  mv "$tmp" "$file"
}

get_env() {
  awk -v key="$1" 'index($0, key "=") == 1 { print substr($0, length(key) + 2); exit }' "$APP_DIR/.env"
}

random_secret() { openssl rand -base64 48 | tr -d '\n/+='; }
random_password() { openssl rand -hex 16; }

write_env() {
  step "Writing the configuration ($APP_DIR/.env)"
  local env_file="$APP_DIR/.env" first_run=false
  if [[ ! -f $env_file ]]; then
    install -m 600 "$APP_DIR/.env.example" "$env_file"
    first_run=true
  fi
  chmod 600 "$env_file"
  # Checked before anything changes, so a refused run leaves the settings as they were.
  if has_domain && ! $STAGING && [[ -n $PAYSTACK_KEY ]]; then
    refuse_demo_data_in_production
  fi

  # Secrets are generated once and never replaced: a new QR_SIGNING_KEY would invalidate
  # every ticket already issued, and new database passwords would lock the app out.
  local key
  for key in JWT_SECRET QR_SIGNING_KEY; do
    [[ -n "$(get_env "$key")" ]] || set_env "$key" "$(random_secret)"
  done
  if $first_run; then
    set_env MYSQL_PASSWORD "$(random_password)"
    set_env MYSQL_ROOT_PASSWORD "$(random_password)"
    set_env BOOTSTRAP_ADMIN_EMAIL "$ADMIN_EMAIL"
    set_env BOOTSTRAP_ADMIN_PASSWORD "$(random_password)"
    set_env LOG_FORMAT json
  fi

  set_env WEB_PORT "$PORT"
  if has_domain; then
    set_env PUBLIC_URL "https://$DOMAIN"
    set_env WEB_BIND 127.0.0.1 # only Caddy reaches the app
    set_env COOKIE_SECURE true
    set_env TRUST_PROXY_HEADERS true # client IPs come from Caddy's X-Forwarded-For
    if [[ "$(get_env MAIL_FROM)" == *"@$PROJECT.local>" ]]; then
      set_env MAIL_FROM "GotATicket <no-reply@$DOMAIN>"
    fi
  else
    set_env PUBLIC_URL "http://$HOST_IP:$PORT"
    set_env WEB_BIND 0.0.0.0 # browsers connect straight to the app
    set_env COOKIE_SECURE false # browsers drop secure cookies on plain HTTP
    set_env TRUST_PROXY_HEADERS false # no proxy in front, so the header could be forged
  fi

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

  # Remembered so a later move to production can refuse to put demo data live.
  if $DEMO; then
    set_env DEMO_DATA true
  fi

  if [[ "$(get_env SMTP_HOST)" == mailpit ]]; then
    warn "emails go to the built-in Mailpit inbox, not to customers. Set SMTP_HOST, SMTP_PORT," \
      "SMTP_USERNAME, SMTP_PASSWORD and SMTP_TLS in $env_file and run this script again."
  fi
  [[ -n "$(get_env DEEPSEEK_API_KEY)" ]] ||
    info "AI features are off until you set DEEPSEEK_API_KEY in $env_file."
}

refuse_demo_data_in_production() {
  [[ "$(get_env DEMO_DATA)" == true ]] || return 0
  die "this server holds demo events, orders and accounts, which must not go live. To start
       with an empty database (this deletes all data and uploaded images), run:
         cd $APP_DIR
         docker compose -f docker-compose.yml -f docker-compose.prod.yml --profile demo down -v
         sed -i '/^DEMO_DATA=/d' .env
       then run this script again. The first super administrator is created again from
       BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD in .env."
}

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
	reverse_proxy 127.0.0.1:$(get_env WEB_PORT)
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

# ---- Start --------------------------------------------------------------------------------

compose() {
  docker compose --project-directory "$APP_DIR" \
    -f "$APP_DIR/docker-compose.yml" -f "$APP_DIR/docker-compose.prod.yml" "$@"
}

start_stack() {
  step "Building and starting GotATicket (the first build takes a few minutes)"
  local profile=()
  $DEMO && profile=(--profile demo)
  compose "${profile[@]}" up -d --build --remove-orphans
  docker image prune -f >/dev/null || true
}

wait_until_healthy() {
  step "Waiting for the app to become healthy"
  local url i
  url="http://127.0.0.1:$(get_env WEB_PORT)/api/v1/health"
  for ((i = 0; i < 90; i++)); do
    if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
      info "Healthy: $url"
      return
    fi
    sleep 2
  done
  compose ps
  compose logs --tail 60 migrate app >&2 || true
  die "the app did not become healthy within 3 minutes; the logs above show why"
}

# ---- Backups ------------------------------------------------------------------------------

install_backups() {
  step "Setting up daily backups ($BACKUP_DIR, kept for 14 days)"
  cat >/usr/local/sbin/$PROJECT-backup <<EOF
#!/usr/bin/env bash
# Backs up the GotATicket database and uploaded images. Installed by deploy-ubuntu.sh.
# Restore the database: gunzip -c db-<time>.sql.gz | docker compose \\
#   -f $APP_DIR/docker-compose.yml -f $APP_DIR/docker-compose.prod.yml exec -T mysql \\
#   sh -c 'MYSQL_PWD="\$MYSQL_ROOT_PASSWORD" mysql -uroot $PROJECT'
set -Eeuo pipefail
umask 077
dest=$BACKUP_DIR
keep_days=\${KEEP_DAYS:-14}
stamp=\$(date +%Y%m%d-%H%M%S)
mkdir -p "\$dest"

docker compose --project-directory $APP_DIR -f $APP_DIR/docker-compose.yml \\
  -f $APP_DIR/docker-compose.prod.yml exec -T mysql \\
  sh -c 'MYSQL_PWD="\$MYSQL_ROOT_PASSWORD" exec mysqldump -uroot --single-transaction --quick --routines --triggers --no-tablespaces $PROJECT' \\
  | gzip >"\$dest/db-\$stamp.sql.gz.part"
mv "\$dest/db-\$stamp.sql.gz.part" "\$dest/db-\$stamp.sql.gz"

docker run --rm -v ${PROJECT}_uploads:/data:ro -v "\$dest":/backup alpine:3 \\
  tar czf "/backup/uploads-\$stamp.tar.gz" -C /data .

find "\$dest" -maxdepth 1 -name '*.gz' -mtime +"\$keep_days" -delete
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

  if [[ "$(get_env SMTP_HOST)" == mailpit ]]; then
    mail="test inbox only: ssh -L 8025:127.0.0.1:8025 <you>@<server>, then open http://localhost:8025"
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
  App folder:    $APP_DIR   (settings in .env)
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
    sudo $APP_DIR/scripts/deploy-ubuntu.sh --domain <your domain> --email <your email> --paystack-key <sk_live_...>
  $( [[ "$(get_env DEMO_DATA)" == true ]] && echo "This server has demo data; the script will ask you to clear it before going live." || echo "Your events, orders and accounts are kept.")
EOF
  fi

  cat <<EOF

  Useful commands (from $APP_DIR):
    docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
    docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f app worker
    /usr/local/sbin/$PROJECT-backup        # back up now
    sudo $APP_DIR/scripts/deploy-ubuntu.sh $rerun   # after changing .env or updating code
EOF
}

main() {
  parse_args "$@"
  check_system
  ensure_swap
  install_base_packages
  install_docker
  if has_domain; then
    install_caddy
  else
    detect_ip
  fi
  fetch_source
  resolve_port
  configure_firewall
  write_env
  start_stack
  wait_until_healthy
  if has_domain; then
    configure_caddy
  fi
  install_backups
  summary
}

main "$@"
