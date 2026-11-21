# API reference

Base path: `/api/v1`. Requests and responses are JSON unless noted.

- **Success:** `{ "data": … }`, plus `"meta": { "page", "per_page", "total" }` on paginated lists (`?page=` and `?per_page=`, at most 100).
- **Errors:** `{ "error": { "code": "validation_failed", "message": "…", "fields": { "email": "is required" } } }` with a matching HTTP status. Common codes: `unauthorized` (401), `forbidden` (403), `not_found` (404), `validation_failed` (422), `rate_limited` (429); 409 conflicts carry a specific code such as `sold_out` or `eft_unavailable`.
- **Authentication:** `Authorization: Bearer <access token>`. Records belonging to an organiser you cannot see return 404.
- **Times** are ISO 8601 in UTC. Event-related inputs ending in `_at` are local times in the event's time zone (`2027-02-10T19:00`); responses include both (`starts_at` and `starts_at_local`).
- **Money** is in cents (`price_cents: 25000` is R 250.00). Percentages for promo codes are in basis points (`1500` = 15%).

## Authentication

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/auth/register` | `{first_name, last_name, email, phone, password}` → session |
| POST | `/auth/login` | `{email, password}` → `{access_token, access_token_expires_at, user}`; sets the refresh cookie (or returns `refresh_token` with `X-Client: mobile`) |
| POST | `/auth/refresh` | Rotates the refresh token (cookie, or `{refresh_token}` for mobile) |
| GET | `/auth/providers` | `{google: true\|false}`: which external sign-in buttons to show |
| GET | `/auth/google/start?next=/path` | Redirects to Google (OpenID Connect with PKCE) |
| GET | `/auth/google/callback` | Google returns here; sets the session cookie and redirects to `/auth/complete?next=…`, or to `/login?sso_error=<code>` |
| POST | `/auth/logout` | Revokes the session |
| POST | `/auth/forgot-password` | `{email}` — always succeeds, to avoid revealing accounts |
| POST | `/auth/reset-password` | `{token, password}` |
| GET / PUT | `/auth/me` | Profile, permissions and organiser memberships / update `{first_name, last_name, phone}` |
| POST | `/auth/change-password` | `{current_password, new_password}` — signs out other sessions |

## Marketplace (public)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/public/events` | `?q=&category=<slug>&province=&city=&max_price=<rand>&from=&to=&sort=date\|popular\|price&page=`; each event includes `sold_out` and `selling_fast` |
| GET | `/public/nearby` | `?lat=&lng=&radius_km=200&limit=12`: upcoming events at venues within the radius (1–500 km), soonest first, each with `distance_km`. Coordinates are rounded to about 1 km |
| GET | `/public/locations` | Provinces and cities that have upcoming events, both in alphabetical order, with `event_count` |
| GET | `/public/places?province=` | Every city, town and settlement in one of the nine provinces, alphabetically, with coordinates and any `former_names` (Port Elizabeth for Gqeberha). Gzip-compressed and cacheable for a day |
| GET | `/public/events/{slug}` | Event with ticket types, availability and sale status |
| GET | `/public/categories` · `/public/payment-options` | |
| GET | `/public/features` | Which AI features are on: `ai_search`, `ai_event_writer`, `ai_event_review` |
| GET | `/public/search/interpret?q=` | Smart search: returns `keywords`, `category`, `province`, `city`, `when` (`today`, `weekend`, `week`, `month`, `custom` or empty), `from`, `to`, `max_price` (rand) and `sort`. 503 with `ai_disabled`, `ai_unavailable` or `ai_limit_reached` when AI can't answer; search normally instead |

## Customers (signed in)

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/orders` | Reserve: `{event_id, items: [{ticket_type_id, quantity}], promo_code}` → pending order with `expires_at` |
| GET | `/orders` · `/orders/{id}` | My orders / one order with items, tickets (with QR payloads), payments, refunds and EFT details |
| POST | `/orders/{id}/checkout` | `{payment_method: "online" \| "eft", attendees: [{order_item_id, first_name, last_name, email, phone}]}` → `{order, next: "redirect" \| "eft" \| "complete", redirect_url, eft}` |
| POST | `/orders/{id}/verify-payment` | Asks the API to check with the gateway (used by the return page) |
| POST | `/orders/{id}/cancel` | Release an unpaid reservation |
| POST | `/orders/{id}/refunds` | `{ticket_ids, reason}` |
| GET | `/orders/{id}/tickets.pdf` | PDF tickets; `?ticket_id=` for one |
| GET | `/tickets` | My tickets; `?when=past` for past events |
| PUT | `/tickets/{id}/attendee` | Change the holder; issues a new QR code |
| POST | `/organisers/apply` | `{name, email, phone, description}` |

## Payment webhooks

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/payments/webhooks/paystack` | Verified with the `x-paystack-signature` header, then re-checked with Paystack |
| POST | `/payments/webhooks/sandbox` | Development gateway only |

## Portal (`/admin`, permission in brackets)

**Dashboard** — `GET /admin/dashboard?event_id=` [dashboard.view]. With `event_id`, every figure covers that event only and the response includes an `event` object; an event outside your organisers returns 404.

**Events**

| Method | Path | Permission |
| --- | --- | --- |
| GET, POST | `/admin/events` (`?status=&q=&organiser_id=`) | events.view, events.create |
| GET, PUT, DELETE | `/admin/events/{id}` | events.view, events.update, events.delete (drafts only) |
| POST | `/admin/events/{id}/submit` · `/withdraw` | events.update |
| POST | `/admin/events/{id}/approve` · `/reject` (`{notes}`) | events.review |
| POST | `/admin/events/{id}/cancel` (`{reason}`) | events.cancel |
| GET, POST | `/admin/events/{id}/ticket-types` | events.view, ticket_types.manage |
| PUT, DELETE | `/admin/ticket-types/{id}` | ticket_types.manage |
| GET, POST | `/admin/events/{id}/promotions` | promotions.manage |
| PUT, DELETE | `/admin/promotions/{id}` | promotions.manage |
| POST | `/admin/events/{id}/complimentary` | tickets.complimentary |
| GET | `/admin/events/{id}/attendees` (`?q=&checkin_state=`) | attendees.view |
| GET | `/admin/events/{id}/checkins` | checkin.scan or attendees.view |
| POST | `/admin/uploads` (multipart `file`, images up to 5 MB) | events.create or events.update |

**Check-in**

| Method | Path | Permission |
| --- | --- | --- |
| GET | `/admin/checkin/events` | checkin.scan |
| POST | `/admin/checkin/scan` — `{event_id, code, direction: "in" \| "out", gate}` → `{result: "accepted" \| "rejected", reason, message, ticket, stats}` | checkin.scan |

`code` is the QR content or a typed ticket code. Rejection reasons: `invalid_code`, `not_found`, `wrong_event`, `not_paid`, `ticket_void`, `refunded`, `event_cancelled`, `too_early`, `event_ended`, `already_checked_in`, `already_used`, `not_checked_in`, `qr_revoked`.

**Orders, payments and refunds**

| Method | Path | Permission |
| --- | --- | --- |
| GET | `/admin/orders` (`?q=&status=&source=&event_id=`) · `/admin/orders/{id}` | orders.view |
| GET | `/admin/orders/{id}/tickets.pdf` | orders.view |
| POST | `/admin/orders/{id}/resend` · `/cancel` | orders.manage |
| PUT | `/admin/tickets/{id}/attendee` | orders.manage |
| POST | `/admin/orders/{id}/refunds` (`{ticket_ids, reason}`) | refunds.manage |
| GET | `/admin/payments` (`?status=&method=&q=`) | payments.view |
| POST | `/admin/payments/{id}/confirm` (`{amount_cents, bank_reference, note}`) | payments.confirm |
| GET | `/admin/refunds` (`?status=&q=`) | refunds.view |
| POST | `/admin/refunds/{id}/approve` · `/reject` (`{note}`) | refunds.manage |
| POST | `/admin/refunds/{id}/process` (`{bank_reference}` for EFT) | refunds.process |

**Reports**

`GET /admin/reports` lists the reports you may run. `GET /admin/reports/{name}?from=YYYY-MM-DD&to=YYYY-MM-DD&event_id=&format=json|csv|xlsx|pdf` runs one; exports need reports.export. Names: `ticket-sales`, `sales-by-category`, `event-performance`, `attendees` (needs `event_id`), `revenue`, `organiser-performance`, `payments`, `refunds`. Each definition carries `event_filter`: `required` (attendees), `none` (organiser-performance, which ignores `event_id`) or `optional` (the rest). An event outside your organisers returns 404.

**Organisers and teams**

| Method | Path | Permission |
| --- | --- | --- |
| GET, POST | `/admin/organisers` | organisers.view, organisers.manage (platform) |
| GET, PUT | `/admin/organisers/{id}` | platform organisers.* or team.manage in that organiser |
| POST | `/admin/organisers/{id}/approve` · `/suspend` · `/reactivate` | organisers.manage (platform) |
| GET, POST | `/admin/organisers/{id}/members` | team.manage |
| PUT, DELETE | `/admin/organisers/{id}/members/{userID}` | team.manage |

**Users, roles, catalogue, audit**

| Method | Path | Permission |
| --- | --- | --- |
| GET, POST | `/admin/users` (`?q=&role_id=&status=`) | users.view, users.manage |
| GET, PUT | `/admin/users/{id}` | users.view, users.manage |
| POST | `/admin/users/{id}/activate` · `/deactivate` · `/password-reset` | users.manage |
| PUT | `/admin/users/{id}/roles` (`{role_ids}`) | users.manage |
| GET | `/admin/roles` (`?scope=platform\|organiser`) · `/admin/roles/{id}` | roles.manage, users.manage or team.manage |
| POST, PUT, DELETE | `/admin/roles`, `/admin/roles/{id}` | roles.manage |
| GET | `/admin/permissions` | roles.manage |
| POST, PUT, DELETE | `/admin/categories`, `/admin/categories/{id}` | categories.manage |
| GET, POST, PUT, DELETE | `/admin/venues`, `/admin/venues/{id}` | venues.manage (GET also with events.view) |
| POST | `/admin/ai/event-draft` (`organiser_id`, `notes`, optional `title`) | events.create or events.update for that organiser. Returns a suggested `title`, `summary`, `description`, `category` and `to_add` questions; saves nothing |
| GET, POST | `/admin/ai/events/{id}/review` | events.review. POST runs the AI pre-check (`verdict`: `ready`, `needs_changes` or `high_risk`, `summary`, `issues`); GET returns the latest one with `stale` |
| GET | `/admin/audit-logs` (`?action=&actor=&entity_type=&entity_id=`) | audit.view |

For South African venues, `province` must be one of the nine provinces (common forms such as `KZN` are accepted) and is filled in from the city when left empty; former city names are updated (Port Elizabeth becomes Gqeberha). `latitude` and `longitude` are optional; when both are left out they come from the city centre.
