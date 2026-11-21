# Deploying to production

GotATicket ships as one image, built from the `Dockerfile` at the repository root. It contains the Go binary and the built React app, so the same image runs every role: `serve` (website, portal and API), `worker`, `migrate` and `seed`. Add MySQL 8.4 and Redis. `docker-compose.yml` runs all of it on one server; the same image runs on any container platform (it listens on port 8080 and has a built-in health check, `gotaticket healthcheck`).

## One Ubuntu server, one command

`scripts/deploy-ubuntu.sh` sets up a fresh Ubuntu 22.04 or 24.04 server (2 vCPU / 4 GB recommended): Docker, a firewall that opens only SSH and the site's ports, a `.env` with random secrets, and daily backups. With a domain it also adds HTTPS through Caddy, with automatic Let's Encrypt certificates.

Copy the project to the server and unzip it (for example `scp gotaticket.zip you@server:`, then `unzip gotaticket.zip && cd gotaticket`), and run the script as root.

**No domain yet: the server's IP address and a port.** The site runs on plain HTTP at `http://<server ip>:<port>`, always as a test site (sandbox payments, or a Paystack `sk_test_` key):

```bash
sudo ./scripts/deploy-ubuntu.sh --port 8000 --email you@example.co.za          # add --demo for demo data
```

The IP address is detected; pass `--ip` to choose it. If your cloud provider has its own firewall or security group, open the port there as well. Browsers keep some features for HTTPS only, so until you have a domain: door staff type ticket codes instead of using the camera; installing the app, offline tickets and "Use my location" don't work; Google sign-in and live payments are unavailable; and passwords travel unencrypted.

**With a domain.** Point its DNS A record at the server first:

```bash
# Live site with Paystack
sudo ./scripts/deploy-ubuntu.sh --domain tickets.example.co.za --email you@example.co.za \
     --paystack-key sk_live_xxx

# Test site with sandbox payments and demo data
sudo ./scripts/deploy-ubuntu.sh --domain staging.example.co.za --email you@example.co.za --staging --demo
```

To move from the IP address to a domain, run the script again with `--domain`: events, orders and accounts are kept. A server that was loaded with demo data refuses to go live and shows how to start with an empty database.

Or let the script clone from git with `--repo git@github.com:<owner>/gotaticket.git` (add the server's SSH key as a read-only deploy key). Run `./scripts/deploy-ubuntu.sh --help` for every option.

What you get:

- The app in `/opt/gotaticket`, settings in `/opt/gotaticket/.env` (readable by root only). It runs with `docker-compose.yml` plus `docker-compose.prod.yml`, which keeps MySQL off the network and binds Mailpit to `127.0.0.1`. With a domain the app also listens on `127.0.0.1` only, behind Caddy (Docker would otherwise publish ports straight through the firewall).
- The first super administrator's sign-in in `/root/gotaticket-admin.txt`. Change the password after signing in, then remove `BOOTSTRAP_ADMIN_PASSWORD` from `.env`.
- Backups of the database and uploaded images every night at 02:30 in `/var/backups/gotaticket`, kept for 14 days (`/usr/local/sbin/gotaticket-backup` runs one now). Copy them off the server as well.

The script is safe to run again. It never replaces the secrets or database passwords in an existing `.env`, so tickets already issued stay valid. Run it again after you change `.env` (SMTP details, `DEEPSEEK_API_KEY`, Google sign-in) or to release a new version: upload the new code to a new folder and run its script, or use `--repo` to pull the latest commit.

## Required settings

The API refuses to start with `APP_ENV=production` unless these are safe:

| Setting | Value |
| --- | --- |
| `APP_ENV` | `production` |
| `PUBLIC_URL` | The site's HTTPS address, e.g. `https://tickets.example.co.za`. Used in emails and payment redirects. |
| `JWT_SECRET`, `QR_SIGNING_KEY` | Two different random values, 32+ characters (`openssl rand -base64 48`). Changing `QR_SIGNING_KEY` invalidates every QR code already issued, so keep it safe and stable. |
| `COOKIE_SECURE` | `true` (requires HTTPS) |
| `PAYMENT_PROVIDER` | `paystack`, with `PAYSTACK_SECRET_KEY` |
| `PASSWORD_HASH_ITERATIONS` | 600000 or more (the default) |

Also set:

- `REDIS_URL` — required when you run more than one API instance (rate limits and the permission cache must be shared).
- `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `MAIL_FROM` — any transactional email provider.
- `EFT_*` — the bank account customers pay into, or `EFT_ENABLED=false`.
- `TRUST_PROXY_HEADERS=true` only when the API sits behind your own proxy (so client IPs for rate limiting come from `X-Forwarded-For`).
- `BOOTSTRAP_ADMIN_EMAIL` / `BOOTSTRAP_ADMIN_PASSWORD` — creates the first super administrator on first start. Remove them afterwards.
- `DEFAULT_COMMISSION_BPS` (500 = 5%), `DEFAULT_CURRENCY` (ZAR), `DEFAULT_TIMEZONE` (Africa/Johannesburg), `RESERVATION_TTL` (15m), `EFT_PAYMENT_WINDOW` (48h), `CHECKIN_OPENS_BEFORE` (4h), `CHECKIN_CLOSES_AFTER_END` (2h) as needed.

## Paystack

1. In the Paystack dashboard, copy the **secret key** into `PAYSTACK_SECRET_KEY` (test keys work for a staging site).
2. Set the **webhook URL** to `https://<your site>/api/v1/payments/webhooks/paystack`.
3. Customers return to `https://<your site>/orders/<id>/payment-return` after paying; that page only asks the API to verify, so nothing depends on it.

## Sign in with Google

1. In the Google Cloud console, open **APIs & Services → Credentials** and create an **OAuth client ID** of type *Web application*.
2. Add the authorised redirect URI `https://<your site>/api/v1/auth/google/callback` (for local development: `http://localhost:5173/api/v1/auth/google/callback`).
3. On the **OAuth consent screen**, add your app name, logo and support email; the scopes needed are `openid`, `email` and `profile`.
4. Set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. The "Continue with Google" button appears on the sign-in and sign-up pages as soon as both are set.

How accounts are matched: a Google account that has signed in before goes to the same GotATicket account. Otherwise, if its verified email matches an existing account, the two are linked; when that account's owner had never proved control of the address (for example by using a password-reset link), its password and sessions are cleared first, so nobody who registered someone else's address keeps access. Anyone else gets a new customer account. People who joined with Google can add a password later with "Forgot your password". With several API instances, Redis is required so the short-lived sign-in state is shared.

## AI features

Smart search, "Write with AI" and the event pre-check use DeepSeek. They are off, and the rest of the platform works exactly as before, until you set the key:

1. Create an API key on the DeepSeek platform and add credit.
2. Set `DEEPSEEK_API_KEY` in `.env` (never in the code or the frontend) and restart: `docker compose up -d`.
3. Check the log line `AI features on`, then try a search such as "comedy in Durban this weekend".

| Setting | Default | Purpose |
| --- | --- | --- |
| `DEEPSEEK_API_KEY` | (empty: AI off) | The key. Only the server uses it |
| `AI_BASE_URL` | `https://api.deepseek.com` | Any OpenAI-compatible API, including a self-hosted DeepSeek model |
| `AI_MODEL` | `deepseek-flash` | Smart search and "Write with AI" |
| `AI_REVIEW_MODEL` | `deepseek-v4-pro` | The event pre-check, which needs more judgement |
| `AI_TIMEOUT` | `30s` | How long to wait for an answer |
| `AI_DAILY_TOKEN_LIMIT` | `2000000` | Tokens per day across the platform before AI pauses until midnight; `0` for no limit |

**Privacy.** DeepSeek's hosted API processes data in China, so these features are built to send no personal information: event details, ticket types, category names and search text only. Email addresses and long numbers (phone, ID, card and account numbers) are removed from anything typed before it is sent, and buyers' names, emails, orders and tickets are never included. Features that would need personal data should run on a self-hosted DeepSeek model in South Africa or the EU (change `AI_BASE_URL`), or have your POPIA adviser's sign-off first.

**Cost and limits.** Every call is recorded in the `ai_usage` table (feature, user, model, tokens, outcome, time taken). Smart search answers are cached for an hour and one- or two-word searches don't use AI at all. Each person is limited per feature (search 20 a minute, "Write with AI" 20 an hour, pre-checks 30 an hour), and the daily token limit caps the platform as a whole. To see today's usage:

```sql
SELECT feature, COUNT(*) AS calls, SUM(prompt_tokens + completion_tokens) AS tokens
FROM ai_usage WHERE created_at >= CURDATE() GROUP BY feature;
```

**When DeepSeek is down**, smart search falls back to the normal keyword search, and the writer and pre-check show a short "try again" message. Nothing else is affected.

## HTTPS

Put a TLS-terminating proxy or load balancer (Caddy, nginx, Traefik, a cloud load balancer) in front of the `app` service (port 8080 in the container), and set `TRUST_PROXY_HEADERS=true`. HTTPS is required for secure cookies, for the scanner to use phone cameras, for installing the app (service workers only run over HTTPS or on localhost) and for "Use my location".

The app server sends the service worker (`/sw.js`), the manifest and the icons with `Cache-Control: no-cache`, so installed apps find a new release on their next check. If your proxy or CDN caches responses, keep that header for those files rather than overriding it.

## Processes

- Run **one or more** `serve` containers with `EMBEDDED_WORKER=false`, and **one or more** `worker` containers. Workers coordinate through the database, so extra copies are safe.
- Run `migrate` once per release before starting the new version, as `docker-compose.yml` does. It is safe to re-run and waits for the database to come up. (Outside Docker, the API can instead migrate on start with `AUTO_MIGRATE=true`; a database lock keeps parallel starts safe.)
- Health check: `GET /api/v1/health`.
- Logs: `LOG_FORMAT=json` writes one JSON object per line with a request ID.

## Data

- **Back up MySQL** daily (for example `mysqldump --single-transaction`) and test restores. All business state, including the email queue, lives there.
- **Uploaded images** live in `STORAGE_DIR` (the `uploads` volume). Back it up too, or put it on shared storage when running several API instances.
- Redis holds only disposable data; it can be restarted at any time.

## Sizing

A single 2 vCPU / 4 GB server comfortably runs everything for hundreds of events and thousands of daily orders. Reservation and check-in requests are short transactions that lock only the rows involved; the busiest moment is usually an on-sale rush, when you can add API containers behind the load balancer.
