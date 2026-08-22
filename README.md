# PayMux

**Open-source payment gateway multiplexer and webhook router.**

PayMux lets several applications share one payment gateway account safely. Each
application gets its own API credentials, its own webhook destination and its
own isolated view of the data — while the merchant keeps a single Midtrans
account and a single notification URL.

```text
Product A ─┐                          ┌─→ Product A
Product B ─┼─→ PayMux ─→ Midtrans ─→ PayMux ─┼─→ Product B
Product C ─┘                          └─→ Product C
```

Current gateway support:

```text
✓ Midtrans (Snap + Core API)
```

PayMux provides:

- payment creation through Snap, with the token and redirect URL your app needs
- transaction management: status sync, cancel, expire, refund, partial refund
- subscriptions where the merchant account has recurring billing activated
- centralised gateway notifications, verified and attributed to their owner
- normalized events delivered to each application, signed with HMAC-SHA256
- automatic retries with backoff, a dead-letter state and manual replay
- strict isolation: one application can never see or touch another's payments
- an operations dashboard for payments, events, deliveries and gateways

---

## Why

Payment gateway configuration is merchant-wide. One notification URL serves
every product you run, so as soon as a second application needs payments you
are stuck choosing between coupling your products together or inventing fragile
`order_id` prefix conventions.

PayMux takes ownership of the gateway integration instead. Applications talk to
PayMux; PayMux talks to the gateway. Because PayMux creates every transaction,
it knows with certainty which application owns each notification — no prefix
parsing, no guessing.

---

## Quick start

Requirements: Docker, or Go 1.25+ and PostgreSQL 15+.

```bash
git clone https://github.com/anggapixa/paymux.git
cd paymux
cp .env.example .env
```

Generate an encryption key and set the first administrator:

```bash
echo "PAYMUX_ENCRYPTION_KEY=$(openssl rand -hex 32)" >> .env
echo "PAYMUX_ADMIN_EMAIL=you@example.com" >> .env
echo "PAYMUX_ADMIN_PASSWORD=a-long-passphrase-you-choose" >> .env
```

Start everything:

```bash
docker compose up -d
```

| Service   | URL                     |
| --------- | ----------------------- |
| API       | <http://localhost:8080> |
| Dashboard | <http://localhost:5173> |

Sign in to the dashboard with the bootstrap credentials, then remove them from
`.env` — they are only used to create the first account.

### Running the backend directly

```bash
make db                      # start only PostgreSQL
go run ./apps/api            # applies migrations on startup
go run ./apps/worker         # delivers webhooks
```

---

## Configure Midtrans

1. In the dashboard, open **Gateways → Add account** and enter your Midtrans
   **merchant ID**, **client key** and **server key**, choosing the **sandbox**
   or **production** environment.
2. In the Midtrans dashboard, set the **Payment Notification URL** to:

   ```text
   https://your-paymux-host/webhooks/midtrans
   ```

The server key is encrypted at rest with `PAYMUX_ENCRYPTION_KEY` and is never
returned by the API — not to applications, and not to the dashboard.

---

## Create an application

In the dashboard, **Applications → New application**, then:

- create an API key — the plaintext is shown **once**, at creation
- add a webhook destination — its signing secret is also shown once

Keys are issued per environment. A `pmx_test_…` key works against a sandbox
gateway account and a `pmx_live_…` key against a production one; PayMux refuses
the mismatch rather than letting a test credential move real money.

---

## Taking a payment

Your backend calls PayMux instead of Midtrans:

```bash
curl -X POST https://your-paymux-host/api/v1/payments \
  -H "Authorization: Bearer pmx_test_…" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-INV-000123" \
  -d '{
    "application_order_id": "INV-000123",
    "amount": 150000,
    "currency": "IDR",
    "customer": {
      "first_name": "John",
      "last_name": "Doe",
      "email": "john@example.com",
      "phone": "+628123456789"
    },
    "items": [
      { "id": "PROD-001", "name": "Example Product", "price": 150000, "quantity": 1 }
    ]
  }'
```

```json
{
  "id": "pay_01K…",
  "gateway": "midtrans",
  "application_order_id": "INV-000123",
  "gateway_order_id": "pmx_01K…",
  "status": "PENDING",
  "snap_token": "…",
  "redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/…",
  "expires_at": null
}
```

Your frontend then opens Snap with the token, or sends the customer to
`redirect_url`. **The Midtrans server key never leaves PayMux.**

Amounts are integers in the currency's minor unit — for IDR that is one rupiah,
so `150000` is Rp 150.000.

### Idempotency

Send an `Idempotency-Key` header on payment creation. A retry with the same key
returns the original payment instead of opening a second transaction. Reusing a
key with a different body is rejected as a conflict.

---

## Receiving events

PayMux delivers a signed JSON event to your destination:

```json
{
  "id": "evt_01K…",
  "type": "payment.paid",
  "gateway": "midtrans",
  "application_id": "app_01K…",
  "payment_id": "pay_01K…",
  "application_order_id": "INV-000123",
  "gateway_order_id": "pmx_01K…",
  "status": "PAID",
  "gateway_status": "settlement",
  "amount": 150000,
  "currency": "IDR",
  "created_at": "2026-08-20T10:00:00Z",
  "gateway_data": { "…": "the gateway's own payload" }
}
```

Headers:

| Header               | Meaning                                  |
| -------------------- | ---------------------------------------- |
| `PayMux-Event-Id`    | the event's identifier                   |
| `PayMux-Delivery-Id` | this delivery attempt series             |
| `PayMux-Event-Type`  | e.g. `payment.paid`                      |
| `PayMux-Timestamp`   | unix seconds, part of the signed payload |
| `PayMux-Signature`   | `v1=<hex hmac-sha256>`                   |
| `PayMux-Attempt`     | attempt number, starting at 1            |

### Verifying the signature

The signed string is `<timestamp>.<delivery id>.<raw request body>`, keyed by
your destination secret. Verify against the **raw** body, before any parsing:

```go
mac := hmac.New(sha256.New, []byte(secret))
fmt.Fprintf(mac, "%s.%s.", timestamp, deliveryID)
mac.Write(body)
expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
ok := hmac.Equal([]byte(expected), []byte(header))
```

Reject deliveries whose timestamp is far from your clock, and treat the event
as at-least-once: PayMux retries until you answer `2xx`, so make your handler
idempotent on `id`.

### Event types

```text
payment.created      payment.pending     payment.authorized
payment.paid         payment.failed      payment.canceled
payment.expired      payment.refunded    payment.partially_refunded

refund.created       refund.completed    refund.failed

subscription.created subscription.updated
subscription.enabled subscription.disabled subscription.canceled
```

### Retries

An attempt that fails is retried on a widening schedule with jitter:

```text
1 min → 5 min → 15 min → 1 hour → 6 hours → 24 hours
```

After seven attempts the delivery is marked **dead** and stays visible in the
dashboard, where it can be replayed. A `4xx` other than 408, 425 or 429 stops
the retries immediately: your endpoint has said the request itself is wrong,
and repeating it will not change that.

---

## API

Applications authenticate with `Authorization: Bearer pmx_live_…`.

```http
POST   /api/v1/payments
GET    /api/v1/payments
GET    /api/v1/payments/{id}
POST   /api/v1/payments/{id}/sync
POST   /api/v1/payments/{id}/cancel
POST   /api/v1/payments/{id}/expire
POST   /api/v1/payments/{id}/snap/cancel
POST   /api/v1/payments/{id}/refunds
GET    /api/v1/payments/{id}/refunds

POST   /api/v1/subscriptions
GET    /api/v1/subscriptions
GET    /api/v1/subscriptions/{id}
PATCH  /api/v1/subscriptions/{id}
POST   /api/v1/subscriptions/{id}/enable
POST   /api/v1/subscriptions/{id}/disable
POST   /api/v1/subscriptions/{id}/cancel

GET    /api/v1/events
GET    /api/v1/deliveries
POST   /api/v1/deliveries/{id}/retry
GET    /api/v1/gateway/capabilities

POST   /webhooks/midtrans        # the gateway's notification URL
GET    /health                   # liveness
GET    /ready                    # readiness, including the database
GET    /metrics                  # Prometheus metrics
```

### Metrics

Both the API and the worker export Prometheus metrics. The API serves them on
its own listener; the worker serves them on `PAYMUX_METRICS_ADDR` (`:9090` by
default), since it has no API of its own.

```text
paymux_http_requests_total{method,route,status}
paymux_http_request_duration_seconds{method,route}
paymux_payments_created_total{gateway,outcome}
paymux_gateway_requests_total{gateway,operation,outcome}
paymux_gateway_request_duration_seconds{gateway,operation}
paymux_webhook_received_total{gateway,routing}
paymux_delivery_total{outcome}
paymux_delivery_failures_total{reason}
paymux_delivery_duration_seconds{outcome}
paymux_delivery_queue_depth{state}
```

Labels are deliberately coarse — route patterns rather than paths, status
classes rather than codes — because a payment identifier in a label would make
the series unbounded. Neither endpoint is authenticated, so keep them off the
public network.

The full contract is in [`docs/openapi.yaml`](docs/openapi.yaml).

### Errors

```json
{
  "error": {
    "code": "PAYMENT_NOT_FOUND",
    "message": "Payment was not found.",
    "request_id": "req_01K…"
  }
}
```

Quote the `request_id` when reporting a problem; it appears in the logs for the
same request.

### Gateway-specific options

Common parameters are normalized. Anything Midtrans-specific goes in a
namespaced, validated block:

```json
{
  "gateway_options": {
    "midtrans": {
      "credit_card": { "secure": true },
      "bank_transfer": { "bank": "bca" },
      "page_expiry_minutes": 30
    }
  }
}
```

Unknown fields are rejected rather than forwarded: PayMux never passes an
unvalidated blob to a payment gateway under your merchant credentials.

---

## Architecture

```text
apps/
  api/          HTTP service: application API, admin API, gateway callbacks
  worker/       webhook delivery and housekeeping
  dashboard/    React + TypeScript + Vite operations UI

internal/
  api/          HTTP handlers, routing, the public wire format
  application/  applications, API keys, webhook destinations
  auth/         administrator sessions and API-key authentication
  payment/      the payment lifecycle and idempotency
  subscription/ recurring billing
  notification/ inbound gateway callbacks: verify, attribute, apply
  event/        the normalized event model
  delivery/     the durable queue, the sender and the worker
  gateway/      the adapter contract and normalized vocabulary
    midtrans/   everything Midtrans-specific
  storage/      PostgreSQL pool, migrations, shared query helpers
  crypto/       at-rest encryption, password and API-key hashing, HMAC
  netsafe/      SSRF protection for outbound webhooks
  money/        exact conversion between minor units and decimal strings

migrations/     versioned SQL schema
docs/           OpenAPI specification and operator documentation
deployments/    Dockerfiles and nginx configuration
```

Gateway-specific code lives only in the adapter. The payment domain speaks the
normalized vocabulary in `internal/gateway`, which is what makes adding a
second gateway a matter of writing an adapter rather than rewriting the core.

---

## Security

- Midtrans notifications are verified against the documented signature before
  anything is applied to a payment.
- Outbound webhooks are signed with per-destination HMAC-SHA256 secrets.
- API keys are stored as SHA-256 hashes; the plaintext is shown once.
- Administrator passwords use Argon2id.
- Gateway server keys and webhook secrets are sealed with AES-256-GCM.
- Outbound webhook destinations are blocked from private, loopback, link-local
  and cloud-metadata ranges, checked again at connection time so a hostname
  cannot change its answer between validation and delivery.
- PayMux never stores card numbers, CVVs or any raw card credential.

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).

---

## Development

```bash
make test              # unit tests
make lint              # go vet + gofmt check
make dashboard-lint    # eslint + typecheck
```

Integration tests need a disposable PostgreSQL database:

```bash
docker run -d --name paymux-test-pg -e POSTGRES_USER=paymux \
  -e POSTGRES_PASSWORD=paymux -e POSTGRES_DB=paymux -p 55432:5432 postgres:17-alpine

PAYMUX_TEST_DATABASE_URL="postgres://paymux:paymux@localhost:55432/paymux?sslmode=disable" \
  make test-integration
```

They exercise the whole path: three applications sharing one gateway account,
a payment taken by one of them, and the signed event reaching only its owner.

See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Status

PayMux is Midtrans-first, and its architecture is gateway-agnostic. The
long-term goal is one integration for applications and several payment gateways
behind it.

## License

[Apache-2.0](LICENSE)
