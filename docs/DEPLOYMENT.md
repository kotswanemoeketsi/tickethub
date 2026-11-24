# Deploying to production

GotATicket ships as one image, built from the `Dockerfile` at the repository root. It contains the Go binary and the built React app, so the same image runs every role: `serve` (website, portal and API), `worker`, `migrate` and `seed`. Add MySQL 8.4 and Redis. `docker-compose.yml` runs all of it on one server; the same image runs on any container platform (it listens on port 8080 and has a built-in health check, `gotaticket healthcheck`).

## One Ubuntu server, one command (nginx, no Docker)

`deploy.sh` sets up GotATicket on an Ubuntu 22.04, 24.04 or 26.04 server. It uses the MySQL database and Redis server you already have, and puts nginx in front of the app. Copy the zip to the server and run:

```bash
unzip gotaticket.zip && cd gotaticket
sudo ./deploy.sh
```

No options are needed. The site opens at `http://<server ip>` as a test site with sandbox payments. Run `sudo ./deploy.sh --help` for the options.

What the script does:

- **Installs** nginx, and Go and Node.js to build the app. Go and Node.js come from go.dev and nodejs.org and are checked against their published checksums. MySQL and Redis are not installed or reconfigured.
- **Database:** it reads the login from `DATABASE_DSN` in `/opt/gotaticket/.env`. If that's missing, it looks in the project's `.env` or `backend/.env`, for `DATABASE_DSN`, `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME`, or `MYSQL_USER`/`MYSQL_PASSWORD`/`MYSQL_DATABASE`. It checks the login before building and stops with the MySQL error if it fails. Nothing in MySQL is changed, except that the database itself is created if it doesn't exist and the user is allowed to create it.
- **Redis:** it uses `REDIS_URL` from `.env`, else Redis on `127.0.0.1:6379`. Without Redis the app still runs, keeping its cache in memory.
- **Settings:** writes `/opt/gotaticket/.env`. Values already there are kept, missing ones are added, and a copy of the previous file is saved as `.env.bak`.
- **Services:** runs the app as two systemd services, `gotaticket-web` and `gotaticket-worker`, under the unprivileged `gotaticket` user. The app listens on `127.0.0.1:8000` only, and nginx forwards port 80 to it.
- **Firewall:** opens SSH and the site's port with ufw. It also sets up daily backups.

**With a domain:** point its DNS A record at the server and fill in the four Ozow credentials in `/opt/gotaticket/.env` (see [Ozow](#ozow)), then run:

```bash
sudo ./deploy.sh --domain tickets.example.co.za --email you@example.co.za --ozow
```

The script first checks that the domain points at this server. If it doesn't yet, the script stops before changing anything and says what to fix. It then gets a Let's Encrypt certificate, which certbot renews automatically. nginx serves HTTPS and redirects plain HTTP to it. If `www.<domain>` also points at the server, it's included in the certificate and redirects to the main domain. Events, orders and accounts are kept. Ports 80 and 443 must be open in your hosting provider's firewall.

**Without a domain**, browsers keep some features for HTTPS only:

- Door staff type ticket codes instead of using the camera.
- Installing the app, offline tickets and "Use my location" don't work.
- Google sign-in and live payments are unavailable.
- Passwords travel unencrypted.

### How a release works

Each run builds a new release in `/opt/gotaticket/releases/<time>-<commit>`. It then migrates the database with the new binary. Only if that succeeds does it point `/opt/gotaticket/current` at the new release and restart both services. If a build or migration fails, the site keeps running the previous release. The last three releases are kept. The secrets in `.env` are never replaced, so tickets already issued stay valid. To release a new version, unzip it on the server and run its `deploy.sh`.

### Day-to-day

| Task | Command |
| --- | --- |
| Status | `systemctl status gotaticket-web gotaticket-worker` |
| Live logs | `journalctl -u gotaticket-web -u gotaticket-worker -f` |
| Apply a change to `.env` | `sudo systemctl restart gotaticket-web gotaticket-worker` |
| Run an app command (`migrate`, `seed`) with the server's settings | `sudo gotaticket migrate` |
| Back up now | `sudo /usr/local/sbin/gotaticket-backup` |
| nginx site | `/etc/nginx/sites-available/gotaticket` (rewritten on each deploy) |

Backups run every night at 02:30 into `/var/backups/gotaticket` and are kept for 14 days. Each backup holds the database, the uploaded images (`/var/lib/gotaticket/uploads`) and `.env`. Copy them off the server as well.

When `SMTP_HOST` is empty, emails are saved as files in `/var/lib/gotaticket/mail` instead of being sent. The first super administrator's sign-in is saved in `/root/gotaticket-admin.txt`. Change the password after signing in, then remove `BOOTSTRAP_ADMIN_PASSWORD` from `.env`.

## Required settings

The API refuses to start with `APP_ENV=production` unless these are safe:

| Setting | Value |
| --- | --- |
| `APP_ENV` | `production` |
| `PUBLIC_URL` | The site's HTTPS address, e.g. `https://tickets.example.co.za`. Used in emails and payment redirects. |
| `JWT_SECRET`, `QR_SIGNING_KEY` | Two different random values, 32+ characters (`openssl rand -base64 48`). Changing `QR_SIGNING_KEY` invalidates every QR code already issued, so keep it safe and stable. |
| `COOKIE_SECURE` | `true` (requires HTTPS) |
| `PAYMENT_PROVIDER` | `ozow`, with `OZOW_CLIENT_ID`, `OZOW_CLIENT_SECRET`, `OZOW_SITE_CODE` and `OZOW_WEBHOOK_SECRET` |
| `PASSWORD_HASH_ITERATIONS` | 600000 or more (the default) |

Also set:

- `REDIS_URL` — required when you run more than one API instance (rate limits and the permission cache must be shared).
- `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `MAIL_FROM` — any transactional email provider.
- `EFT_*` — the bank account customers pay into, or `EFT_ENABLED=false`.
- `TRUST_PROXY_HEADERS=true` only when the API sits behind your own proxy (so client IPs for rate limiting come from `X-Forwarded-For`).
- `BOOTSTRAP_ADMIN_EMAIL` / `BOOTSTRAP_ADMIN_PASSWORD` — creates the first super administrator on first start. Remove them afterwards.
- `DEFAULT_COMMISSION_BPS` (350 = 3.5%; plus R5 per paid ticket), `DEFAULT_CURRENCY` (ZAR), `DEFAULT_TIMEZONE` (Africa/Johannesburg), `RESERVATION_TTL` (15m), `EFT_PAYMENT_WINDOW` (48h), `CHECKIN_OPENS_BEFORE` (4h), `CHECKIN_CLOSES_AFTER_END` (2h) as needed.

## Ozow

1. In the Ozow dashboard, create a **One API** client with **payments** and **refunds** scopes.
2. Set `OZOW_CLIENT_ID`, `OZOW_CLIENT_SECRET`, `OZOW_SITE_CODE` and `OZOW_WEBHOOK_SECRET` in the protected server environment file (`/opt/gotaticket/.env` for the Ubuntu script). Keep `OZOW_BASE_URL=https://one.ozow.com/v1` for production.
3. Configure a **full** `transaction.complete` webhook to `https://<your site>/api/v1/payments/webhooks/ozow`. Use its Svix signing secret for `OZOW_WEBHOOK_SECRET`.
4. Set `PAYMENT_PROVIDER=ozow`, or deploy with `--ozow` after entering those settings. The deployment script keeps sandbox payments for local/staging demos.
5. Customers return to `https://<your site>/orders/<id>/payment-return`. The backend verifies payment independently before issuing tickets.

See [OZOW.md](OZOW.md) for testing, migration, refunds and existing-payment handling. Apply migration `0005_ozow.sql` using the normal application migration command. The Ubuntu deployment script does this before restarting the app.

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
