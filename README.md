# GotATicket

A multi-organiser event ticketing marketplace. Organisers sign up, create events, sell tickets online or by EFT, and scan tickets at the door; the platform team approves events, manages payments and refunds, and earns a commission on every sale.

- **Backend:** Go 1.24 modular monolith (standard library HTTP, MySQL, optional Redis, JWT), with a background worker.
- **Frontend:** React 19 + TypeScript + Tailwind. One app holds the public marketplace, the customer account, the staff/organiser portal and a phone-friendly QR scanner.
- **Payments:** Paystack for card and instant EFT, manual bank EFT with staff confirmation, and a built-in sandbox gateway for development.

## What's included

| Area | Highlights |
| --- | --- |
| Marketplace | Featured-event carousel, "selling fast" and per-category rows, search, browse with date/category/city filters and sorting, event pages with live availability, directions, add-to-calendar and sharing, promo codes, a reservation countdown at checkout, attendee details per ticket, help centre and terms of sale |
| Sign-in | Email and password, or **Continue with Google** (OpenID Connect with PKCE); see [Sign in with Google](docs/DEPLOYMENT.md#sign-in-with-google) |
| Payments | Online payments are confirmed only by server-to-server verification (signed webhook *and* a verify call), never by the browser. Manual EFT with a 48-hour pay-by window. Late and duplicate payments are refunded or re-allocated automatically |
| Tickets | One signed QR code per ticket (HMAC, cannot be forged), PDF tickets emailed to each attendee, ticket transfer that revokes the old QR code, complimentary tickets |
| Inventory | Row-locked reservations that make overselling impossible (tested with 30 concurrent buyers racing for the last 10 tickets), reservations expire and return stock automatically |
| Check-in | Camera or typed-code scanning, checks that the ticket exists, is paid, belongs to the event, is not already used and is inside the check-in window; re-entry (check-out/in) per event; live attendance |
| Refunds | Customer requests, staff-initiated refunds, approval step, gateway or bank-transfer payout, automatic full refunds when an event is cancelled |
| Organisers | Self-service applications with platform approval, team members with organiser-scoped roles, strict data isolation between organisers, per-organiser commission |
| Events | Draft → submitted → approved/rejected → published → cancelled workflow, ticket types (VIP, Early Bird, …) with sale windows and order limits, images, venues, categories |
| Users & roles | Built-in roles for each job (super admin, admin, finance, ticketing, check-in, organiser, customer) plus custom roles from a permission checklist (for example a Marketing Manager), activate/deactivate, password resets, protection against privilege escalation |
| Reports | Ticket sales, sales by category, event performance, attendees, revenue, organiser performance, payments and refunds — on screen or exported to Excel, CSV or PDF |
| AI (DeepSeek) | Smart search that turns "comedy in Durban this weekend under R300" into filters, "Write with AI" for event listings, and an AI pre-check that flags problems and scam signs before an admin approves an event. Off until an API key is set; no personal information is sent; see [AI features](docs/DEPLOYMENT.md#ai-features) |
| App (PWA) | Installs to the home screen on Android, iPhone and desktop, opens without a connection, and keeps the signed-in user's upcoming tickets on the device so their QR codes can be shown at a venue with no signal. Asks before switching to a new release |
| Audit | Every sensitive change (prices, approvals, refunds, role grants, payment confirmations, check-ins) is logged with who, when and what changed |

## Quick start with Docker

All you need is Docker with Compose v2 (Docker Desktop, or Docker Engine on Linux).

```bash
docker compose --profile demo up -d --build
```

The first build takes a few minutes. When it finishes:

| What | Where |
| --- | --- |
| Marketplace and portal | <http://localhost:8000> |
| Every email the app sends (tickets, receipts, password resets) | <http://localhost:8025> (Mailpit) |
| MySQL, for a database client | `localhost:3307`, user `gotaticket`, password `gotaticket` |

`--profile demo` loads demo organisers, 21 events with artwork, six weeks of orders and an account for every role (see the table below). Leave it out for an empty platform, and sign in as **admin@gotaticket.local** with password **ChangeMe2026**.

What runs:

| Service | Role |
| --- | --- |
| `mysql` | MySQL 8.4, data in the `mysql-data` volume |
| `redis` | Cache, rate limits and sign-in state |
| `mailpit` | Catches outgoing email |
| `migrate` | Applies the database migrations (`backend/migrations/*.sql`, built into the image), then exits. Nothing else starts until it succeeds |
| `app` | The website, the portal and the API on port 8000 |
| `worker` | Sends emails, expires reservations, reconciles payments and refunds |
| `seed` | Demo data, only with `--profile demo` and only into an empty database |

Everyday commands (or the `make up`, `make demo`, `make down`, `make reset` and `make logs` shortcuts):

```bash
docker compose logs -f app worker              # follow the logs
docker compose --profile demo down             # stop (your data is kept)
docker compose --profile demo down -v          # stop and delete the database and uploads
docker compose up -d --build                   # rebuild after changing the code
docker compose run --rm migrate                # apply new migrations by hand
```

Every setting has a working default. To change one (ports, the admin account, Paystack or Google keys, bank details, SMTP), copy `.env.example` to `.env` and edit it. One image (`Dockerfile` at the repository root) runs every service: it builds the React app and the Go binary and ships them in a 32 MB image with no shell or operating system, running as a non-root user.

## Quick start without Docker

This path suits a modest laptop: MySQL is the only service you need. Redis and an SMTP server are optional — without them the API uses an in-process cache and writes emails (with PDF tickets attached) to `backend/storage/mail` as `.eml` files you can open in any mail app.

**You need:** Go 1.24+, Node.js 20.19+ (or 22.12+), and MySQL 8.0.16 or newer. The [setup guide](docs/SETUP.md) has step-by-step instructions for macOS, including older Macs.

```bash
make setup     # installs Go and npm dependencies, creates backend/.env
make db        # creates the databases and the `gotaticket` MySQL user (asks for the root password)
make seed      # loads demo organisers, events, orders and accounts
make api       # terminal 1 — the API on http://localhost:8080
make web       # terminal 2 — the app on http://localhost:5173
```

Open <http://localhost:5173>.

## Demo accounts

Every demo account uses the password **`Password123`**:

| Account | Sees |
| --- | --- |
| `admin@gotaticket.test` | Platform administrator: everything except super-admin settings |
| `finance@gotaticket.test` | Payments, EFT confirmation, refunds, financial reports |
| `ticketing@gotaticket.test` | Orders, attendees, complimentary tickets |
| `checkin@gotaticket.test` | The scanner, for any event |
| `organiser@gotaticket.test` | Organiser portal for *Soweto Sound Productions* |
| `organiser2@gotaticket.test` | Organiser portal for *Cape Town Comedy Collective* (cannot see the other organiser's data) |
| `doorstaff@gotaticket.test` | Scanner for Soweto Sound Productions' events only |
| `marketing@gotaticket.test` | A custom *Marketing Manager* role: events, dashboard and sales reports, read-only |
| `customer@gotaticket.test` | A ticket buyer with past orders |
| `superadmin@gotaticket.test` | Super administrator (native setup; with Docker, the super administrator is `admin@gotaticket.local`) |

To buy tickets, sign in as the customer, pick an event and pay: the sandbox gateway shows its own checkout page where you can choose **Pay now** or **Decline payment**. To try the scanner, sign in as `checkin@…`, open **Scan tickets**, and type the code printed under a ticket's QR code or scan the QR from another screen.

> Browsers only allow camera access on `https://` pages or `localhost`. To scan with a phone during development, either type codes or expose the app over HTTPS (for example with a tunnelling tool).


## Project layout

```
backend/
  cmd/gotaticket/         one binary: serve | worker | migrate | seed
  internal/
    app/                 wiring, routes, worker schedule, integration tests
    auth, identity/      JWTs, sessions, users, roles
    rbac/                permission catalogue and built-in roles
    organisers/          organisers and their teams
    events/, catalog/    events, ticket types, promo codes, venues, categories
    sales/               reservations, checkout, payments, tickets, refunds
    payments/            gateway interface: Paystack and the sandbox
    tickets/             signed QR codes and PDF tickets
    checkin/             scanning rules and attendance
    reports/, dashboard/ reporting and exports (CSV, Excel, PDF)
    notifications/       email templates; delivery goes through the job queue
    audit/               audit trail
    platform/            shared infrastructure: database, HTTP, cache, jobs, mail…
  migrations/            SQL schema, embedded in the binary
frontend/src/
  pages/public|account|admin|scanner
  components/, layouts/, lib/
docs/                    setup, architecture and API notes
Dockerfile               one image for every service
docker-compose.yml       the full stack: MySQL, Redis, Mailpit, migrate, app, worker, demo seed
```

## Tests

```bash
make test          # Go unit + integration tests, TypeScript checks and frontend tests
```

The Go integration tests run the whole application against a real MySQL database (`gotaticket_test`, created by `make db`) and cover overselling under concurrency, the payment and webhook flow, reservation expiry and late payments, every check-in rule, organiser data isolation, custom roles and privilege escalation, the refund lifecycle, EFT, event cancellation, promo code limits and session security. They are skipped automatically if the test database is unreachable; point `TEST_DATABASE_DSN` elsewhere if needed.

## Going live

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md). In short: set `APP_ENV=production`, real `JWT_SECRET` and `QR_SIGNING_KEY`, `COOKIE_SECURE=true` behind HTTPS, `PAYMENT_PROVIDER=paystack` with your secret key, a real SMTP provider, Redis, and your bank details for EFT. The API refuses to start in production with unsafe settings. To set up a whole Ubuntu server with HTTPS and backups in one go, run `scripts/deploy-ubuntu.sh` (see [One Ubuntu server, one command](docs/DEPLOYMENT.md#one-ubuntu-server-one-command)).

## Documentation

- [docs/SETUP.md](docs/SETUP.md) — installing the prerequisites (macOS, Linux) and troubleshooting
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how purchases, payments, check-in and permissions work, and why
- [docs/API.md](docs/API.md) — endpoint reference
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) — production configuration and operations

## Third-party data

The list of South African cities, towns and settlements comes from
[dirkstrauss/SouthAfrica](https://github.com/dirkstrauss/SouthAfrica), © 2017 Dirk Strauss,
used under the MIT licence. The data, its licence and the changes the application makes
when loading it are in `backend/internal/platform/geo/data/`.
