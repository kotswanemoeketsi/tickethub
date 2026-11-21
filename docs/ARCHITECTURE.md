# Architecture

GotATicket is a **modular monolith**: one Go binary, one MySQL database, with each business area in its own package behind a service type. Modules call each other through Go interfaces and share one transaction when they must change data together. This keeps deployment simple (one image, scale by running more copies) while the boundaries stay clear enough to split a module out later if one ever needs to scale on its own.

```
             ┌───────────── browser / phone ─────────────┐
             │  React app: marketplace · account ·        │
             │  organiser & staff portal · QR scanner     │
             └──────────────────┬─────────────────────────┘
                                │ HTTPS (one origin)
                                │
   Paystack ──webhooks──► ┌─────▼──────────────────────────┐
            ◄─verify/refund│ gotaticket serve  (stateless)   │──► Redis (cache, rate limits)
                           │ web app (static) · /api        │
                           │ identity · rbac · organisers   │
                           │ events · catalog · sales       │──► MySQL (all state, job queue)
                           │ payments · tickets · checkin   │
                           │ reports · audit · notifications│
                           └─────▲──────────────────────────┘
                                 │ same code, different command
                           ┌─────┴──────────┐
                           │ gotaticket worker│──► SMTP (tickets, receipts, notices)
                           └────────────────┘
```

## Request handling

Handlers are thin: decode and validate input, call a service, write JSON in a `{ "data": … }` or `{ "error": { "code", "message", "fields" } }` envelope. Services own the rules and the transactions. Errors carry an HTTP status and a stable `code`, so the frontend can show field-level messages without parsing text.

Every request passes through request IDs, structured logging, panic recovery, security headers, optional CORS, and per-IP rate limits on sign-in, registration and password reset.

## Identity, roles and permissions

- **Sessions.** Sign-in returns a 15-minute JWT access token (kept in memory by the web app) and a refresh token in an `HttpOnly`, `SameSite=Strict` cookie scoped to `/api/v1/auth`. Refresh tokens rotate on every use; presenting an already-used token revokes the whole session family (theft detection). Mobile clients can send `X-Client: mobile` to receive the refresh token in the body instead. Passwords are hashed with PBKDF2-SHA256 (600,000 iterations in production) and transparently re-hashed when the setting increases. Ten failed sign-ins lock an account for 15 minutes; successful sign-ins never count.
- **Permissions** are fine-grained codes (`events.create`, `refunds.process`, `reports.export`, …) defined in code and synced to the database at start-up. **Roles** are named sets of permissions. Built-in roles cannot be edited; custom roles can be created from the permission checklist in the portal.
- **Two scopes.** *Platform roles* (Super Admin, Admin, Finance Manager, Ticketing Officer, Check-in Officer, Customer, custom roles) apply across the platform. *Organiser roles* (Event Organiser, Organiser Check-in Staff, custom organiser roles) apply only inside one organiser, through team membership. A platform role sees every organiser's data only if it holds `platform.all_organisers`.
- **Data isolation.** Every query for organiser-owned data is filtered by the caller's scope for the specific permission, built once per request from a cached principal (`Principal.Scope(permission)`). Asking for another organiser's record returns 404, not 403, so IDs cannot be probed.
- **No privilege escalation.** You can only grant roles whose permissions you hold yourself; only a super admin can create another super admin; an organiser team must always keep someone who can manage the team.
- Role or membership changes invalidate the principal cache immediately, and deactivating an account revokes its sessions.

## Events

Events move through `draft → pending_review → published`, with `rejected` (organiser edits and resubmits) and `cancelled`. Only active organisers can submit; platform reviewers approve or reject with notes. Times are entered in the event's own time zone and stored in UTC; the API returns both. A published event's derived *phase* (`upcoming`, `live`, `completed`) drives what buyers and scanners can do.

Ticket types carry price, quantity, per-order limits and optional sale windows. Changing a price is allowed (it affects new orders only) and is written to the audit log.

## Buying tickets

```
reserve ──► checkout ──► pay ──► verify ──► issue tickets ──► email
  │            │                    │
  │            │                    └─ webhook (signed) and/or return-page verify; both re-check with the gateway
  │            └─ attendee details, payment method; online gets a redirect URL, EFT gets bank details
  └─ stock held for 15 minutes
```

1. **Reserve.** The API locks the ticket type rows (`SELECT … FOR UPDATE`, always in ascending ID order to avoid deadlocks), checks availability, limits, sale windows and promo code usage, then increments `quantity_reserved`. A database `CHECK (quantity_sold + quantity_reserved <= quantity_total)` makes overselling impossible even if a bug slipped past the application check. Transactions that hit a deadlock or lock timeout are retried automatically.
2. **Checkout.** Attendee details are saved per ticket. Online payment extends the hold to at least 10 more minutes (never beyond 45 minutes from the reservation) and returns the gateway's checkout URL. EFT extends the hold to 48 hours, but never closer than 2 hours before the event, and returns the bank details and the order reference to use.
3. **Payment confirmation never trusts the browser.** The return page only asks the API to *verify*. Payments are confirmed when the gateway says so server-to-server: Paystack webhooks are checked against the HMAC signature and then re-verified with Paystack's API before anything changes. Webhooks are de-duplicated. A reconciliation task re-checks pending payments every 2 minutes in case a webhook is lost.
4. **Issuing tickets.** On confirmation the reservation becomes a sale, one ticket per attendee is created with a unique code, and email jobs are queued in the same transaction (transactional outbox), so an email is never sent for a sale that rolled back, and never lost for one that committed.
5. **Expiry.** Every 30 seconds the worker expires unpaid reservations and returns their stock.
6. **Edge cases.** A payment that arrives after the reservation expired re-reserves the stock if it is still available, and is refunded automatically if not. A second successful payment for the same order is refunded. A payment that completes after the event was cancelled is refunded.

**EFT** orders are confirmed by finance staff, who enter the amount received and the bank statement reference; the amount must match the order total.

## Tickets and check-in

Each ticket has a 10-character code (Crockford base-32, shown as `XXXXX-XXXXX`) and a QR code containing `GT1.<code>.<signature>`, where the signature is an HMAC over the code and the ticket's QR version. A forged or altered QR fails verification without a database lookup. Changing a ticket's holder bumps the version, so the old QR stops working.

A scan locks the ticket row and checks, in order: the QR signature, that the ticket exists, belongs to the selected event, is paid and valid (not refunded or void), the event is not cancelled, check-in is open (from 4 hours before the start to 2 hours after the end by default), and the entry state — a second entry is refused unless the event allows re-entry and the attendee checked out. Every scan, accepted or rejected, is recorded with the gate and the staff member, which feeds attendance reports.

## Refunds

Customers can request refunds (if the event allows it and the cut-off has not passed); staff can create them directly. Requests are approved or rejected, then processed: online payments are refunded through the gateway (and reconciled if the gateway answers later), EFT refunds are recorded with the transfer reference once finance has paid the customer. Refunded tickets are voided immediately so they cannot be used. Cancelling an event voids all tickets, emails holders, and creates an approved full refund for every paid order.

## Background work

`gotaticket worker` (or the API itself with `EMBEDDED_WORKER=true`) runs:

| Task | Every |
| --- | --- |
| Send queued emails and other jobs (`SELECT … FOR UPDATE SKIP LOCKED`, retries with back-off) | continuously |
| Expire unpaid reservations | 30 s |
| Reconcile pending payments with the gateway | 2 min |
| Reconcile processing refunds | 5 min |
| Recover jobs stuck after a crash | 5 min |
| Purge completed jobs older than 14 days | 6 h |

Several workers can run at once; `SKIP LOCKED` keeps them from processing the same job.

## Reports

Reports are SQL aggregations scoped to what the caller may see, returned as typed columns (`money`, `int`, `percent`, `datetime`, …) so the same table renders on screen and exports to CSV (with protection against spreadsheet formula injection), Excel (`.xlsx`, written natively) or PDF. Exporting requires `reports.export`; financial reports require `reports.financial`.

## Auditing

Sensitive actions write an audit entry inside the same transaction as the change: who, when, from which IP, which organiser, a human-readable summary and a before/after diff. Audit entries cannot be edited through the API.

## Caching

Redis (optional in development) holds rate-limit counters, the permission cache and short-lived public event listings. Public listings are invalidated by a version bump whenever an event or ticket type changes, so buyers never see stale availability for long. Without Redis, an in-process cache is used — fine for one API process.

## AI features

The `ai` module talks to DeepSeek through its OpenAI-compatible chat API in JSON mode, so the provider can be swapped (for a self-hosted DeepSeek model, say) by configuration alone. Three rules shape it:

- **The model only suggests.** It never writes to the database. Smart search answers become ordinary filters, "Write with AI" fills in the form for the organiser to edit and save, and the pre-check advises a reviewer who still approves or rejects.
- **Every answer is checked.** Categories must exist, cities and provinces must be real (via the place list), dates are worked out in code ("this weekend" is always the same Friday to Sunday as the browse filter), prices must be positive, and text is cut to the form's limits. The pre-check's verdict is made consistent with its issues: a high-severity risk always means "high risk". Anything that doesn't fit is dropped, not passed on.
- **What organisers write is untrusted.** It is sent as JSON data, separate from the instructions, and the pre-check is told to report any attempt to instruct it as a risk.

Personal details are removed before text leaves the platform (see [AI features](DEPLOYMENT.md#ai-features)). The pre-check sees the event, its ticket types and the organiser as a business (name, account age, past events), never people's contact details. Its result is stored with a hash of what was checked, so a reviewer can see when the event has changed since. Running a pre-check is written to the audit log.

## Frontend

One React app, code-split so buyers download only the marketplace; the portal and the scanner load on demand. TanStack Query handles fetching and caching. The access token lives in memory only and is refreshed transparently from the cookie. The portal hides what the user cannot do, but the API enforces every rule; the frontend is never trusted. The scanner uses the phone camera, gives audible and haptic feedback, and ignores repeated reads of the same code for 3 seconds.

### Installable app and offline use

The site is a progressive web app (`vite-plugin-pwa`, Workbox). The service worker precaches the app shell (pages, scripts, styles and the Latin fonts), so the app opens offline. At run time it caches only public, shared data: event listings and categories (network first, falling back to the last copy), the per-province place lists and event images. Requests tied to a signed-in user, such as orders, tickets and the whole portal, are never stored by the service worker.

Tickets are the exception that matters at the door, where signal is often poor. The signed-in user's upcoming tickets, with their signed QR payloads, are copied to the device's local storage whenever they load. Offline, the session cannot be checked, so instead of sending the user to the sign-in page the app shows that copy. The copy belongs to one user: it is deleted when they sign out, when another user signs in on the device, and when the server reports the session has ended. A QR payload on the device is no more powerful than the same code on a PDF or a screenshot: the scanner still checks every ticket against the server, so a cancelled or transferred ticket is refused at the door.

A new release is downloaded in the background, and the app shows "A new version of the app is ready" with a Reload button instead of switching mid-checkout. Installed apps also check for a release every hour.
