# Ozow payments for gotaticket.co.za

This integration uses Ozow One API Hosted Checkout. Customers leave gotaticket to authorise payment on Ozow, then return to their order. The server creates the request, verifies notifications and independently checks the payment through Ozow before issuing tickets. Secrets remain on the server.

## Integration

Ozow replaces the previous live payment provider in this repository. The sandbox remains available for local development. No credentials are stored in the source. Apply the included migration before running the updated app.

## Configure Ozow

1. Sign in to the Ozow dashboard and obtain the site code for gotaticket.co.za.
2. Under **One API > Clients**, create a client with **payments** and **refunds** scopes. Save its Client ID and Client Secret securely. These are One API credentials, not the older Payments API private/API keys.
3. Under that client's **Manage Webhooks**, add **transaction.complete**, with message type **full**, targeting:

   `https://gotaticket.co.za/api/v1/payments/webhooks/ozow`

4. Obtain the webhook signing secret and store it as `OZOW_WEBHOOK_SECRET`. Its value starts with `whsec_`. Use the dashboard or the One API Get Webhook Secret operation with the required webhooks permission. The running application's token requests only payments and refunds.
5. Do not configure a thin subscription or a legacy `notifyUrl` for this handler. It verifies Svix headers and requires the full notification fields. Refund outcomes are polled by the existing worker; a refund webhook is not required.

Ozow currently documents One API webhooks as beta. This implementation also checks payment status on the buyer's return and through the reconciliation worker. Keep the worker running. Pay by Bank is enabled by default; other payment methods depend on Ozow account activation. Refunds require sufficient Ozow float.

## Server configuration

Set these in the server's protected environment file or secret manager. Never place secrets in frontend/VITE variables, screenshots, source control or chat.

```dotenv
PAYMENT_PROVIDER=ozow
OZOW_CLIENT_ID=<One API client ID>
OZOW_CLIENT_SECRET=<One API client secret>
OZOW_SITE_CODE=<gotaticket site code>
OZOW_WEBHOOK_SECRET=<Svix signing secret>
OZOW_BASE_URL=https://one.ozow.com/v1
PUBLIC_URL=https://gotaticket.co.za
API_URL=https://gotaticket.co.za
DEFAULT_CURRENCY=ZAR
```

Retain the application's existing production, database, authentication, email and bank settings. The Docker Compose environment passes Ozow settings to both app and worker. Migration `0005_ozow.sql` adds `payments.provider_request_id`; apply it using the application's normal migration process before serving checkout. Auto-migration applies it on startup when enabled.

For an existing Docker deployment, after reviewing and applying the code and environment changes, use the site's usual release process. With this repository's Compose setup that is:

```sh
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

For the normal Ubuntu deployment, enter the credentials in `/opt/gotaticket/.env`, then run:

```sh
sudo ./deploy.sh --domain gotaticket.co.za --email <your-email> --ozow
```

The deployment applies migrations and restarts both services. Later deployments preserve an already configured Ozow environment. `--staging` deliberately selects the local sandbox. The old provider's deployment flag is no longer supported. No deployment or live payment is performed merely by editing this repository.

## Application behaviour

- Request amounts come from the server-side order. Ozow expects rand values; integer cents are converted exactly, without floating-point rounding.
- Checkout expiry follows the ticket reservation deadline. The returned checkout URL is used as supplied by Ozow, and the separate request ID is saved before redirecting the customer.
- Full webhook messages are verified against raw bytes using the signing secret, timestamp window and all supported signatures. Duplicate message IDs have stable deduplication keys. Existing transactional fulfilment prevents repeated ticket delivery.
- The API status check requires matching site and merchant reference. Existing order fulfilment also requires matching amount and currency. Unknown, incomplete, failed-attempt and refunded transaction states cannot issue tickets. Failed attempts leave reconciliation pending because the buyer can retry on the same hosted request.
- Test notifications are rejected. Do not use a production order to simulate success. Use a separate staging application/database and Ozow staging credentials for supported testing, then an explicitly authorised real low-value purchase for live acceptance.
- The app checks status on the return page and reconciles pending payments through the worker. Multiple successful transactions on one request are flagged for manual reconciliation rather than silently choosing one.
- Refund requests use the original transaction ID and a stable refund reference as the idempotency key. The worker checks refund completion. Requests associated with a previous provider are not sent to Ozow.

## Before activation

Complete tests against a separate MySQL test database, including migration, checkout ID persistence, webhook/return races, duplicate notifications, incorrect amount, cancellation, reservation expiry and ticket email delivery. The existing test harness recreates its test schema: never point `TEST_DATABASE_DSN` at production.

Run a full purchase with your Ozow account: create an order, follow checkout, verify the order becomes paid exactly once, receive tickets and confirm the transaction in the Ozow dashboard. Also test an abandoned/failed payment and a refund. No live transaction was made while preparing this package.

If switching from Paystack, settle outstanding checkouts and refunds before the switch. The application still selects one active gateway; old-provider notifications and automatic verification are not processed after switching. Historical refunds must be handled through the original provider. The previous provider adapter and credentials have been removed; historical database records remain unchanged.

## Verification performed

The new offline tests exercise token requests and reuse, checkout and refund idempotency headers, exact amount conversion, transaction identity checks, non-success statuses, pagination, signed webhook verification, replay rejection, secret rotation, duplicate message keys and configuration validation. The frontend TypeScript check passed using the locally installed dependencies. Database-backed integration tests require a reachable separate MySQL test database; live account credentials and production delivery have not been tested.

## References

- [Choosing a checkout experience](https://hub.ozow.com/integration-methods/apis/money-in/)
- [Take a payment with One API](https://hub.ozow.com/bundles/take-a-payment/)
- [Verify a webhook](https://hub.ozow.com/integration-methods/apis/money-in/verify-a-webhook/)
- [Create a payment request](https://hub.ozow.com/api-reference/one-api/post-payments/)
- [Transaction schema](https://hub.ozow.com/api-reference/one-api/schemas/transaction/)
- [Refund a payment](https://hub.ozow.com/integration-methods/apis/payment-operations/refund-a-payment/)
