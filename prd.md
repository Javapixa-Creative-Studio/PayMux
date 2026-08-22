# PayMux — Product Requirements Document

## 1. Product Overview

**Product Name:** PayMux
**Project Type:** Open-source, self-hosted payment infrastructure
**Primary Gateway for V1:** Midtrans
**Backend:** Go / Golang
**Dashboard:** React + TypeScript + Vite
**Database:** PostgreSQL
**Architecture:** Gateway-adapter based, extensible to additional payment gateways

PayMux is an open-source payment gateway abstraction and webhook orchestration platform.

Its initial purpose is to allow multiple independent applications to use a single Midtrans merchant account while maintaining separate transaction ownership, payment creation, webhook delivery, transaction management, and operational visibility.

Instead of every product communicating independently with Midtrans:

```text
Product A ──→ Midtrans
Product B ──→ Midtrans
Product C ──→ Midtrans

                  │
                  ▼
         Single Notification URL
```

all applications communicate through PayMux:

```text
                        ┌──────────────────────┐
                        │      Midtrans        │
                        │                      │
                        │ Snap / Core API      │
                        │ Notifications        │
                        └──────────┬───────────┘
                                   │
                                   ▼
                        ┌──────────────────────┐
                        │       PayMux         │
                        │                      │
                        │ Payment API          │
                        │ Gateway Adapter      │
                        │ Transaction Store    │
                        │ Webhook Router       │
                        │ Retry Engine         │
                        └──────────┬───────────┘
                                   │
                  ┌────────────────┼────────────────┐
                  ▼                ▼                ▼
              Product A        Product B        Product C
```

PayMux must support the complete Midtrans integration lifecycle required by downstream applications, including **Snap payment creation**, rather than functioning only as a webhook proxy.

---

# 2. Core Problem

A company can operate several products while using one Midtrans merchant account.

For example:

```text
Product A
Product B
Product C
```

All three products need Midtrans.

However, payment gateway configuration such as notification handling is centralized at the merchant-account level.

Without an intermediary payment platform, applications become coupled to each other or must implement fragile routing conventions.

PayMux introduces a centralized payment infrastructure layer:

```text
Applications
     │
     ▼
   PayMux
     │
     ▼
  Midtrans
```

and:

```text
Midtrans
     │
     ▼
   PayMux
     │
     ├──→ Product A
     ├──→ Product B
     └──→ Product C
```

---

# 3. Product Vision

PayMux should eventually provide a unified API over multiple payment gateways.

V1:

```text
Applications
     │
     ▼
   PayMux
     │
     ▼
  Midtrans
```

Future:

```text
                    ┌──→ Midtrans
                    ├──→ Xendit
Applications ─→ PayMux ├──→ Stripe
                    ├──→ DOKU
                    └──→ Other gateways
```

Applications should eventually integrate with:

```text
PayMux API
```

rather than directly depending on a specific gateway SDK.

The initial implementation remains heavily focused on Midtrans, but gateway-specific behavior must remain inside the Midtrans adapter.

---

# 4. Product Goals

PayMux must:

1. Support multiple applications using one Midtrans merchant account.
2. Provide APIs for creating Midtrans payments.
3. Support Midtrans Snap.
4. Support Midtrans transaction lifecycle APIs needed by applications.
5. Receive Midtrans notifications centrally.
6. Securely verify incoming Midtrans events.
7. Determine which PayMux application owns each transaction.
8. Normalize gateway-specific transaction data.
9. Persist transaction and event history.
10. Forward events reliably to application webhooks.
11. Retry failed deliveries.
12. Prevent uncontrolled duplicate processing.
13. Provide an operational dashboard.
14. Provide application-specific API credentials.
15. Make each application isolated from other applications.
16. Be simple to self-host.
17. Be production-oriented.
18. Be open-source friendly.
19. Support Sandbox and Production Midtrans environments.
20. Allow future payment gateways through adapters.

---

# 5. Important Architectural Change

Applications SHOULD create Midtrans transactions through PayMux whenever possible.

Instead of:

```text
Product
   │
   ▼
Midtrans
```

use:

```text
Product
   │
   ▼
PayMux
   │
   ▼
Midtrans
```

This gives PayMux authoritative knowledge of transaction ownership.

Example:

```text
Product B
    │
    │ Create Payment
    ▼
PayMux
    │
    │ Snap API
    ▼
Midtrans
```

Later:

```text
Midtrans
    │
    │ payment notification
    ▼
PayMux
    │
    │ transaction belongs to Product B
    ▼
Product B
```

This is much more reliable than determining ownership exclusively from `order_id` prefixes.

---

# 6. Technology Stack

## Backend

Use:

```text
Go
```

Prefer the current stable Go release supported by the project's CI environment.

The backend should favor Go standard-library capabilities where practical and avoid unnecessary framework dependency.

A lightweight HTTP router/framework may be selected if it materially improves maintainability.

Candidates include:

```text
chi
gin
fiber
echo
```

Preferred direction:

```text
net/http + chi
```

unless repository constraints justify another choice.

Reasons:

* lightweight
* idiomatic Go
* straightforward middleware
* easy testing
* low framework coupling
* good fit for infrastructure services

---

# 7. Frontend Stack

Admin dashboard:

```text
React
TypeScript
Vite
```

Recommended supporting stack:

```text
React
TypeScript
Vite
React Router
TanStack Query
```

Use a maintainable component architecture.

Avoid unnecessary frontend state-management libraries unless application complexity requires them.

Server-state should preferably be handled using:

```text
TanStack Query
```

Local UI state should primarily use React-native state patterns.

---

# 8. Database

Primary database:

```text
PostgreSQL
```

PostgreSQL will store:

* applications
* application credentials
* gateway configurations
* transactions
* orders
* gateway events
* webhook destinations
* deliveries
* delivery attempts
* routing configuration
* audit metadata

Database migrations MUST be version-controlled.

---

# 9. Queue / Background Processing

Webhook delivery and retry must run asynchronously.

Recommended initial approach:

```text
Go Worker
+
PostgreSQL-backed job queue
```

A PostgreSQL-backed queue is preferred for V1 if it provides sufficient reliability.

This reduces infrastructure requirements:

```text
PayMux API
PayMux Worker
PostgreSQL
```

instead of immediately requiring:

```text
Redis
RabbitMQ
Kafka
```

The queue implementation must support:

* durable jobs
* delayed retries
* concurrency
* worker locking
* crash recovery
* retry scheduling

The implementation may later migrate to Redis, NATS, RabbitMQ, or another dedicated queue without changing domain-level logic.

---

# 10. Suggested Repository Structure

A monorepo is recommended.

```text
paymux/
│
├── apps/
│   ├── api/
│   ├── worker/
│   └── dashboard/
│
├── internal/
│   ├── application/
│   ├── auth/
│   ├── payment/
│   ├── transaction/
│   ├── gateway/
│   ├── webhook/
│   ├── delivery/
│   ├── routing/
│   ├── queue/
│   └── storage/
│
├── migrations/
├── docs/
├── deployments/
├── docker-compose.yml
├── .env.example
├── README.md
├── CONTRIBUTING.md
├── SECURITY.md
└── LICENSE
```

Dashboard:

```text
apps/dashboard/
├── src/
│   ├── api/
│   ├── components/
│   ├── features/
│   ├── hooks/
│   ├── layouts/
│   ├── pages/
│   ├── routes/
│   └── types/
├── package.json
└── vite.config.ts
```

---

# 11. Gateway Architecture

Every payment gateway must implement a common adapter contract.

Conceptually:

```go
type Gateway interface {
    Name() string

    CreatePayment(ctx context.Context, req CreatePaymentRequest) (*Payment, error)

    GetTransaction(ctx context.Context, id string) (*Transaction, error)

    CancelTransaction(ctx context.Context, id string) error

    ExpireTransaction(ctx context.Context, id string) error

    RefundTransaction(ctx context.Context, req RefundRequest) (*Refund, error)

    VerifyWebhook(ctx context.Context, req WebhookRequest) error

    ParseWebhook(ctx context.Context, req WebhookRequest) (*GatewayEvent, error)
}
```

Capabilities that are not universally supported should use explicit capability interfaces rather than forcing every gateway to implement meaningless methods.

Example:

```go
type SubscriptionGateway interface {
    CreateSubscription(...)
    GetSubscription(...)
    UpdateSubscription(...)
    EnableSubscription(...)
    DisableSubscription(...)
    CancelSubscription(...)
}
```

---

# 12. Midtrans Adapter

V1 must include:

```text
internal/gateway/midtrans/
```

Suggested structure:

```text
midtrans/
├── client.go
├── adapter.go
├── snap.go
├── core.go
├── transaction.go
├── notification.go
├── subscription.go
├── refund.go
├── verifier.go
├── mapper.go
├── types.go
└── errors.go
```

The rest of PayMux should not directly depend on Midtrans request/response structures.

---

# 13. Midtrans Environment Support

Must support:

```text
Sandbox
Production
```

Configuration should make the environment explicit.

Never accidentally send sandbox requests to production or vice versa.

Each gateway account should record:

```text
environment
merchant_id
client_key
server_key
enabled
```

Sensitive values must be encrypted at rest where practical.

---

# 14. Snap Payment Support

Snap is a REQUIRED V1 feature.

Applications must be able to request a Snap transaction through PayMux.

Example:

```http
POST /api/v1/payments
```

Request:

```json
{
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
    {
      "id": "PROD-001",
      "name": "Example Product",
      "price": 150000,
      "quantity": 1
    }
  ]
}
```

PayMux resolves the authenticated application automatically.

PayMux sends the appropriate request to Midtrans Snap.

Response:

```json
{
  "id": "pay_xxxxxxxxx",
  "gateway": "midtrans",
  "application_order_id": "INV-000123",
  "gateway_order_id": "pmx_xxxxxxxxx",
  "status": "pending",
  "snap_token": "xxxxxxxxx",
  "redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/...",
  "expires_at": null
}
```

---

# 15. Snap Token

PayMux must return the Snap token generated by Midtrans.

Applications may then invoke Midtrans Snap on their frontend.

Conceptual flow:

```text
Product Frontend
       │
       ▼
Product Backend
       │
POST /payments
       ▼
PayMux
       │
       ▼
Midtrans Snap
       │
       ▼
Snap Token
       │
       ▼
Product Backend
       │
       ▼
Product Frontend
       │
       ▼
snap.pay(token)
```

Never expose the Midtrans Server Key to product frontends.

---

# 16. Snap Redirect Flow

PayMux must also expose the Snap redirect URL returned by Midtrans.

This allows applications to use:

```text
redirect checkout
```

instead of:

```text
snap.js popup
```

Applications choose which UX they prefer.

---

# 17. Snap Advanced Parameters

PayMux must NOT unnecessarily restrict Midtrans Snap capabilities.

The internal Midtrans adapter should support Midtrans Snap parameters such as:

* transaction details
* item details
* customer details
* enabled payments
* credit-card options
* bank-transfer configuration
* callbacks
* expiry
* page expiry
* custom fields
* metadata supported by Midtrans
* recurring-related configuration where available
* other officially supported Snap options

The public PayMux API may expose commonly used parameters directly and provide a controlled mechanism for advanced gateway-specific options.

---

# 18. Gateway-Specific Options

PayMux requires a balance between portability and complete gateway support.

Recommended request design:

```json
{
  "application_order_id": "INV-001",
  "amount": 150000,

  "customer": {},

  "gateway": "midtrans",

  "gateway_options": {
    "midtrans": {}
  }
}
```

This allows PayMux's common API to remain normalized while preserving access to Midtrans-specific features.

Do NOT expose arbitrary unvalidated JSON directly to the Midtrans API.

Gateway options must be typed and validated.

---

# 19. Payment Methods

PayMux must not artificially limit Midtrans payment methods supported by Snap.

Applications should be able to use payment channels activated for their Midtrans merchant account.

Depending on Midtrans configuration, this may include categories such as:

```text
Credit/debit cards
Virtual accounts / bank transfer
GoPay
QRIS
ShopeePay
Convenience-store payments
Cardless credit
Direct debit
Other payment methods supported by Midtrans
```

Actual availability depends on the merchant account and Midtrans activation.

PayMux MUST derive behavior from Midtrans capabilities rather than assuming every method is available.

---

# 20. Enabled Payment Methods

Applications may optionally specify permitted payment methods when creating Snap transactions.

Example:

```json
{
  "enabled_payment_methods": [
    "gopay",
    "bca_va",
    "bni_va"
  ]
}
```

PayMux maps these to Midtrans's appropriate API representation.

If omitted:

```text
use merchant's Midtrans configuration
```

---

# 21. Internal Payment ID

Never rely on external `order_id` as the primary database identifier.

PayMux should generate its own identifier:

```text
pay_01K...
```

Suggested ID categories:

```text
app_
pay_
txn_
evt_
dlv_
key_
dst_
sub_
rfd_
```

UUIDv7 or another sortable high-entropy identifier is recommended.

---

# 22. Application Order ID vs Gateway Order ID

Store both.

Example:

```text
application_order_id:
INV-2026-001

gateway_order_id:
pmx_01K...
```

This prevents collisions between independent applications.

For example:

```text
Product A → INV-001
Product B → INV-001
```

are valid because PayMux generates unique gateway identifiers.

---

# 23. Metadata

Payments should support application-defined metadata.

Example:

```json
{
  "metadata": {
    "customer_id": "CUS-123",
    "plan_id": "premium",
    "internal_reference": "SUB-882"
  }
}
```

PayMux should persist metadata.

Metadata can be included in downstream PayMux events.

Do not automatically forward sensitive metadata to Midtrans unless required.

---

# 24. Midtrans Core API

Although Snap is the primary V1 payment creation interface, the architecture should support Midtrans Core API features required for full Midtrans compatibility.

Gateway-specific functions should not leak directly throughout PayMux's business domain.

Separate:

```text
PayMux Payment API
```

from:

```text
Midtrans Adapter
    ├── Snap
    └── Core API
```

---

# 25. Transaction Status

PayMux must understand the Midtrans transaction lifecycle.

Relevant statuses include Midtrans statuses such as:

```text
pending
capture
settlement
deny
cancel
expire
failure
refund
partial_refund
authorize
```

Do not assume this list is permanently exhaustive.

Use Midtrans documentation as the source of truth during implementation.

Store:

```text
gateway_status
```

separately from:

```text
normalized_status
```

---

# 26. Normalized Payment Status

PayMux may expose normalized statuses such as:

```text
PENDING
AUTHORIZED
PAID
FAILED
CANCELED
EXPIRED
REFUNDED
PARTIALLY_REFUNDED
```

Example:

```text
Midtrans settlement
        ↓
PayMux PAID

Midtrans capture
        ↓
PayMux PAID or AUTHORIZED
```

The exact mapping must account for Midtrans transaction semantics such as fraud status and capture behavior.

Do not oversimplify credit-card states.

---

# 27. Transaction Status Query

Expose:

```http
GET /api/v1/payments/{payment_id}
```

and optionally:

```http
POST /api/v1/payments/{payment_id}/sync
```

`sync` queries Midtrans for the latest authoritative transaction state.

Normal reads should preferably return locally persisted PayMux state.

---

# 28. Cancel Transaction

Expose:

```http
POST /api/v1/payments/{payment_id}/cancel
```

PayMux:

```text
authenticate application
      ↓
verify transaction ownership
      ↓
invoke Midtrans
      ↓
persist new state
      ↓
return result
```

---

# 29. Expire Transaction

Expose:

```http
POST /api/v1/payments/{payment_id}/expire
```

Midtrans supports transaction expiration for both Snap and Core API integrations.

The adapter must use the official Midtrans endpoint and semantics.

---

# 30. Snap Session Cancellation

Snap session cancellation must be supported separately from transaction cancellation where applicable.

Conceptual API:

```http
POST /api/v1/payments/{payment_id}/snap/cancel
```

PayMux stores the Snap session/token references necessary to execute the operation.

---

# 31. Refund Support

PayMux must support Midtrans refunds where the merchant/account/payment method supports them.

Endpoints:

```http
POST /api/v1/payments/{payment_id}/refunds
GET  /api/v1/payments/{payment_id}/refunds
```

Request:

```json
{
  "amount": 50000,
  "reason": "Customer requested refund"
}
```

Support:

```text
full refund
partial refund
```

when Midtrans supports the relevant operation.

Refunds require their own persistent entity.

---

# 32. Refund Entity

Store:

```text
id
payment_id
gateway_refund_id
amount
reason
status
gateway_status
created_at
updated_at
raw_response
```

---

# 33. Subscription / Recurring Payments

PayMux architecture must support Midtrans recurring and subscription capabilities.

This should include supported Midtrans operations such as:

```text
create subscription
retrieve subscription
update subscription
enable subscription
disable subscription
cancel subscription
```

where available for the merchant account.

Some recurring capabilities require activation or additional approval from Midtrans.

PayMux must not claim that recurring functionality is available merely because the software supports the API.

---

# 34. Subscription API

Conceptual endpoints:

```http
POST   /api/v1/subscriptions
GET    /api/v1/subscriptions/{id}
PATCH  /api/v1/subscriptions/{id}
POST   /api/v1/subscriptions/{id}/enable
POST   /api/v1/subscriptions/{id}/disable
POST   /api/v1/subscriptions/{id}/cancel
```

Each subscription belongs to exactly one PayMux application.

---

# 35. Saved Payment / Tokenization

Where Midtrans supports tokenization, recurring, one-click, or two-click card functionality, PayMux architecture must permit those flows.

PayMux MUST NOT store raw card information.

Never store:

```text
PAN / card number
CVV
raw sensitive card credentials
```

Store only tokens returned through supported Midtrans tokenization flows where appropriate.

---

# 36. PCI Scope

PayMux should minimize PCI DSS scope.

PayMux must not implement custom collection/storage of card credentials.

Prefer:

```text
Midtrans Snap
Midtrans-hosted/payment-tokenization interfaces
```

for card-sensitive operations.

---

# 37. Notification Endpoint

Expose:

```http
POST /webhooks/midtrans
```

This becomes the Midtrans Payment Notification URL.

Flow:

```text
Midtrans
   │
   ▼
Receive Notification
   │
   ▼
Validate
   │
   ▼
Verify Authenticity
   │
   ▼
Identify Transaction
   │
   ▼
Idempotency Check
   │
   ▼
Persist
   │
   ▼
Update Payment State
   │
   ▼
Generate PayMux Event
   │
   ▼
Queue Destination Delivery
   │
   ▼
Return HTTP Success
```

Do not wait for application webhook delivery before acknowledging Midtrans.

---

# 38. Midtrans Notification Verification

Verification MUST follow the current official Midtrans security specification.

Do not implement Midtrans verification from assumptions or outdated blog posts.

Implementation should account for the appropriate combination of:

```text
signature verification
transaction lookup where appropriate
server-side credentials
```

according to current Midtrans documentation.

---

# 39. Webhook Idempotency

Midtrans may send duplicate notifications.

Receiving the same payment state repeatedly must not create uncontrolled duplicate events.

Use stable gateway transaction information and database uniqueness constraints.

Example conceptual key:

```text
gateway
transaction_id
transaction_status
fraud_status
```

Exact implementation should reflect actual Midtrans semantics.

---

# 40. Event Ordering

Payment events may:

* arrive more than once
* arrive delayed
* be processed concurrently
* represent status transitions

PayMux must prevent stale events from incorrectly downgrading transaction state.

Example:

```text
settlement received
      ↓
payment = PAID

delayed pending notification received
      ↓
DO NOT incorrectly revert payment to PENDING
```

Implement an explicit state-transition policy.

---

# 41. Normalized Event Model

Example:

```json
{
  "id": "evt_01K...",
  "type": "payment.paid",
  "gateway": "midtrans",
  "application_id": "app_01K...",
  "payment_id": "pay_01K...",
  "application_order_id": "INV-001",
  "gateway_order_id": "pmx_01K...",
  "gateway_transaction_id": "...",
  "status": "PAID",
  "gateway_status": "settlement",
  "amount": 150000,
  "currency": "IDR",
  "created_at": "2026-08-20T10:00:00Z",
  "data": {}
}
```

---

# 42. Event Types

Define stable PayMux event names.

Examples:

```text
payment.created
payment.pending
payment.authorized
payment.paid
payment.failed
payment.denied
payment.canceled
payment.expired
payment.refunded
payment.partially_refunded

subscription.created
subscription.updated
subscription.enabled
subscription.disabled
subscription.canceled

refund.created
refund.completed
refund.failed
```

Do not force downstream applications to interpret raw Midtrans statuses unless they want to.

The original Midtrans information should remain available in:

```text
gateway_data
```

or an equivalent field.

---

# 43. Application Webhook

Each registered product gets an independent destination.

Example:

```text
Product A:
https://product-a.example.com/webhooks/paymux

Product B:
https://product-b.example.com/webhooks/paymux

Product C:
https://product-c.example.com/webhooks/paymux
```

Only the owning application receives its payment event.

---

# 44. Outbound Signing

Every webhook sent by PayMux should contain an HMAC signature.

Suggested headers:

```text
PayMux-Event-ID
PayMux-Delivery-ID
PayMux-Timestamp
PayMux-Signature
```

or standardized `X-` variants if preferred.

Suggested signature:

```text
HMAC-SHA256
```

using the application's webhook secret.

Document the canonical signing string.

---

# 45. Delivery Flow

```text
PayMux Event
    │
    ▼
Delivery Queue
    │
    ▼
Worker
    │
    ▼
Application
```

Successful delivery:

```text
HTTP 2xx
→ SUCCESS
```

Failure:

```text
network error
timeout
HTTP non-2xx
    ↓
retry
```

---

# 46. Retry Policy

Recommended default:

```text
Attempt 1 → immediate
Attempt 2 → 1 minute
Attempt 3 → 5 minutes
Attempt 4 → 15 minutes
Attempt 5 → 1 hour
Attempt 6 → 6 hours
Attempt 7 → 24 hours
```

Add jitter where appropriate to prevent synchronized retry storms.

After maximum attempts:

```text
DEAD
```

---

# 47. Manual Replay

Administrators should be able to replay failed webhook deliveries.

Endpoint:

```http
POST /api/v1/deliveries/{delivery_id}/retry
```

Dashboard action:

```text
Retry
```

Eventually PayMux may provide full historical webhook replay.

---

# 48. Application Authentication

Each application gets one or more API keys.

Example:

```text
pmx_live_xxxxxxxxx
pmx_test_xxxxxxxxx
```

The API key determines application ownership.

Therefore a normal request does NOT need to trust a user-supplied `application_id`.

Example:

```http
Authorization: Bearer pmx_live_xxxxx
```

PayMux resolves:

```text
API key
   ↓
Application B
```

---

# 49. Application Isolation

Product A MUST NOT be able to:

* read Product B transactions
* cancel Product B transactions
* refund Product B transactions
* inspect Product B webhook events
* manipulate Product B subscriptions
* access Product B secrets

Authorization must be enforced server-side.

Never rely on dashboard filtering for isolation.

---

# 50. Admin Authentication

The dashboard requires administrative authentication.

V1 may use:

```text
email + password
```

with securely hashed passwords.

Preferred password hashing:

```text
Argon2id
```

or another modern password-hashing algorithm appropriate for Go.

Future versions may add:

```text
OIDC
OAuth
SSO
RBAC
```

---

# 51. Dashboard

Dashboard implementation:

```text
React
TypeScript
Vite
```

Primary navigation:

```text
Overview
Applications
Payments
Transactions
Subscriptions
Refunds
Events
Deliveries
Gateways
Settings
```

---

# 52. Dashboard — Overview

Show operational metrics such as:

```text
Payments today
Total payment value
Successful payments
Pending payments
Failed payments

Webhook deliveries
Failed deliveries
Pending retries

Unrouted notifications
```

Do not turn V1 into a complex business-intelligence platform.

Focus on payment operations.

---

# 53. Dashboard — Applications

Administrators can:

```text
Create application
Edit application
Disable application
Create API key
Revoke API key
Configure webhook destination
Rotate webhook secret
```

---

# 54. Dashboard — Payments

Display:

```text
PayMux Payment ID
Application
Application Order ID
Gateway
Gateway Transaction ID
Payment Method
Amount
Currency
Status
Gateway Status
Created At
Updated At
```

Filters:

```text
Application
Status
Gateway
Date
Order ID
Transaction ID
```

---

# 55. Payment Detail

Payment detail should include:

```text
Payment summary
Customer information
Items
Gateway information
Snap information
Payment method
Transaction status
Refunds
Gateway events
PayMux events
Webhook deliveries
Metadata
Raw gateway payload
```

Sensitive data must be redacted.

Actions where permitted:

```text
Sync Status
Cancel
Expire
Refund
Cancel Snap Session
Replay Webhook
```

---

# 56. Dashboard — Events

Event explorer should display:

```text
Event ID
Event Type
Gateway
Application
Payment
Gateway Transaction
Received At
Routing Status
Delivery Status
```

---

# 57. Dashboard — Deliveries

Display:

```text
Delivery ID
Application
Event
Destination
State
Attempts
Last HTTP Status
Last Error
Response Time
Next Retry
```

---

# 58. Gateway Configuration

Dashboard:

```text
Gateways
    │
    └── Midtrans
```

Midtrans configuration:

```text
Environment
Merchant ID
Client Key
Server Key
Enabled
Connection Status
```

Secrets should be write-only after configuration.

For example:

```text
Server Key
••••••••••••••
```

Never return the original value through the API.

---

# 59. Multi-Gateway Future Compatibility

Do not put Midtrans credentials directly in generic application-domain tables.

Model:

```text
gateway_accounts
```

Possible future records:

```text
midtrans-main
xendit-main
stripe-global
```

Initially there will normally only be:

```text
Midtrans
```

---

# 60. Suggested Database Entities

Core schema:

```text
admins

applications
application_api_keys
webhook_destinations

gateway_accounts

payments
payment_items
payment_customers

gateway_transactions
gateway_events

refunds
subscriptions

events

deliveries
delivery_attempts

audit_logs
```

---

# 61. Payments Table

Suggested conceptual fields:

```text
id
application_id
gateway_account_id

application_order_id
gateway_order_id

amount
currency

normalized_status
gateway_status

payment_method
payment_type

metadata

created_at
updated_at
paid_at
expired_at
canceled_at
```

Create appropriate unique indexes.

At minimum:

```text
(application_id, application_order_id)
```

should normally be unique.

---

# 62. Raw Gateway Data

Preserve relevant raw gateway requests/responses for debugging and auditing.

However:

* secrets must be removed
* authorization headers must never be stored
* sensitive card information must never be stored
* personally identifiable information should be minimized
* retention should eventually be configurable

Prefer JSONB for structured gateway payload storage in PostgreSQL.

---

# 63. Transaction Creation Idempotency

Application requests can also be duplicated.

Support:

```http
Idempotency-Key: ...
```

for payment creation.

Same:

```text
application
+
idempotency key
```

should produce the same logical payment rather than creating multiple Midtrans transactions.

This is critical.

---

# 64. API Error Format

Provide consistent errors.

Example:

```json
{
  "error": {
    "code": "PAYMENT_NOT_FOUND",
    "message": "Payment was not found.",
    "request_id": "req_01K..."
  }
}
```

Never expose:

* PostgreSQL errors
* stack traces
* Midtrans Server Key
* Go panic information
* internal SQL
* raw infrastructure errors

---

# 65. Request IDs

Every incoming API request should receive a request identifier.

Example:

```text
req_01K...
```

Use it consistently across:

```text
API response
logs
database operations where useful
worker logs
```

---

# 66. Observability

Use structured logging.

Recommended:

```text
log/slog
```

unless another logging package is justified.

Logs should include fields such as:

```text
request_id
application_id
payment_id
event_id
delivery_id
gateway
```

Never log credentials.

---

# 67. Metrics

Prepare the backend so future Prometheus metrics can be added.

Useful metrics include:

```text
paymux_http_requests_total
paymux_payments_created_total
paymux_gateway_requests_total
paymux_webhook_received_total
paymux_delivery_total
paymux_delivery_failures_total
paymux_delivery_duration_seconds
```

Prometheus support may be included in V1 if straightforward.

---

# 68. Health Checks

Provide:

```http
GET /health
```

for liveness.

And:

```http
GET /ready
```

for dependency readiness.

Readiness should verify essential dependencies such as PostgreSQL.

---

# 69. Graceful Shutdown

Go API and workers must support graceful shutdown.

During termination:

```text
stop accepting new work
finish or safely release active jobs
close HTTP servers
close database connections
```

Do not lose webhook jobs during normal deployment.

---

# 70. Concurrency

Go workers should process independent deliveries concurrently.

Concurrency must be configurable.

Example:

```text
PAYMUX_WORKER_CONCURRENCY=20
```

Concurrency controls must preserve:

* transaction correctness
* idempotency
* ordering requirements
* database integrity

---

# 71. Database Transactions

Use PostgreSQL transactions for operations involving multiple dependent writes.

Example incoming webhook:

```text
BEGIN

insert gateway event
update payment
create PayMux event
create delivery job

COMMIT
```

Where queue implementation permits, ensure durable job creation is transactionally consistent with event persistence.

---

# 72. Security

Security requirements are non-negotiable.

Must include:

```text
Midtrans notification verification
HMAC outgoing signatures
API key hashing
encrypted gateway secrets
administrator password hashing
authorization
input validation
secure HTTP headers
rate limiting where relevant
request-size limits
timeouts
SSRF protection
```

---

# 73. SSRF Protection

PayMux sends requests to user-configurable webhook destinations.

Therefore webhook destinations create an SSRF risk.

Production deployments should prevent destinations from targeting restricted network ranges by default.

Examples:

```text
127.0.0.0/8
169.254.0.0/16
private network ranges
cloud metadata endpoints
```

Allow explicit administrator-controlled exceptions for private self-hosted environments if required.

DNS rebinding considerations must also be evaluated.

---

# 74. HTTP Client

Use a shared properly configured Go HTTP client.

Configure:

```text
connection timeout
TLS handshake timeout
response-header timeout
overall request timeout
connection pooling
max idle connections
```

Do not instantiate an unbounded new HTTP client for every request.

---

# 75. Midtrans Client

Do not scatter direct HTTP calls throughout the application.

Implement one coherent Midtrans client beneath the adapter.

Example:

```go
type Client struct {
    BaseURL    string
    ServerKey  Secret
    HTTPClient *http.Client
}
```

---

# 76. Midtrans SDK Decision

The coding agent must evaluate the current official Midtrans Go SDK before deciding whether to use it.

Criteria:

```text
maintenance status
supported Midtrans features
API completeness
type quality
error handling
testability
dependency footprint
```

If the official SDK does not expose required functionality cleanly, implementing a small internal HTTP client is acceptable.

Do not sacrifice PayMux architecture merely to conform to an SDK.

---

# 77. API Versioning

Use:

```text
/api/v1/
```

from the start.

Examples:

```http
POST /api/v1/payments
GET  /api/v1/payments
GET  /api/v1/payments/{id}

POST /api/v1/payments/{id}/cancel
POST /api/v1/payments/{id}/expire
POST /api/v1/payments/{id}/refunds

POST /api/v1/subscriptions
GET  /api/v1/subscriptions/{id}

GET /api/v1/events
GET /api/v1/deliveries
```

Gateway callbacks remain outside normal application API authentication:

```http
POST /webhooks/midtrans
```

because they use gateway-specific verification.

---

# 78. OpenAPI

Generate or maintain an OpenAPI specification.

Provide:

```text
docs/openapi.yaml
```

The API contract should be treated as part of the public OSS interface.

Avoid changing published V1 behavior casually.

---

# 79. Docker

Provide:

```text
Dockerfile API
Dockerfile Dashboard
docker-compose.yml
```

A default deployment should approximately contain:

```text
paymux-api
paymux-worker
paymux-dashboard
postgres
```

Run:

```bash
docker compose up -d
```

to start a usable development environment.

---

# 80. Production Deployment

Production should permit:

```text
Reverse Proxy / Load Balancer
          │
     ┌────┴─────┐
     ▼          ▼
PayMux API   Dashboard
     │
     ▼
PostgreSQL
     │
     ▼
Workers
```

API instances should be horizontally scalable where possible.

Do not keep essential transaction state only in process memory.

---

# 81. Configuration

Example:

```env
PAYMUX_ENV=production

PAYMUX_HTTP_ADDR=:8080

DATABASE_URL=postgres://...

PAYMUX_ENCRYPTION_KEY=...

PAYMUX_WORKER_CONCURRENCY=20

PAYMUX_LOG_LEVEL=info
```

Midtrans credentials may initially be bootstrapped through environment variables, but the architecture should support encrypted gateway configuration stored through the dashboard.

---

# 82. Open-Source Repository Requirements

Required:

```text
README.md
LICENSE
CONTRIBUTING.md
SECURITY.md
CODE_OF_CONDUCT.md
.env.example
docker-compose.yml
docs/
```

Recommended license:

```text
Apache-2.0
```

or:

```text
MIT
```

Final license choice should be intentional.

---

# 83. README Positioning

Use:

# PayMux

**Open-source payment gateway multiplexer and webhook router.**

PayMux lets multiple applications safely share payment gateway infrastructure through one centralized API.

Current support:

```text
✓ Midtrans
```

Architecture:

```text
Apps → PayMux → Midtrans
                 │
Apps ← PayMux ←──┘
```

PayMux provides:

```text
payment creation
Snap checkout
transaction management
webhook routing
event normalization
automatic retries
delivery logging
application isolation
```

---

# 84. V1 Required Midtrans Scope

The coding agent MUST treat these as V1 requirements where supported by Midtrans APIs and merchant capability:

### Payments

* Snap transaction creation
* Snap token
* Snap redirect URL
* Snap customization/options
* payment-method selection
* customer details
* item details
* custom fields
* transaction expiry configuration

### Transactions

* transaction status
* notification handling
* cancel transaction
* expire transaction
* transaction synchronization

### Snap

* create Snap transaction
* cancel Snap session where supported
* redirect checkout
* Snap.js-compatible token response

### Refunds

* refund
* partial refund when supported
* refund status tracking

### Recurring / Subscription

* Midtrans-supported subscription lifecycle
* tokenization architecture
* one-click/two-click/recurring compatibility where activated

### Notification

* verification
* status lifecycle handling
* deduplication
* durable persistence
* event normalization

### Payment Channels

Do not artificially restrict Midtrans-supported payment methods.

---

# 85. Midtrans Capability Registry

Because features vary by merchant configuration, PayMux should eventually expose gateway capabilities.

Example:

```json
{
  "gateway": "midtrans",
  "capabilities": {
    "snap": true,
    "refund": true,
    "partial_refund": true,
    "subscriptions": false
  }
}
```

Do not hard-code account-specific capability assumptions.

---

# 86. Testing

Testing is mandatory.

## Go Unit Tests

Test:

```text
domain state transitions
Midtrans mappings
signature verification
normalization
routing
idempotency
retry policy
authorization
```

## Integration Tests

Test:

```text
PostgreSQL repositories
transaction isolation
worker locking
queue behavior
API endpoints
```

## Midtrans Sandbox Tests

Where CI secrets permit:

```text
create Snap transaction
query status
expire transaction
notification flow
```

Do not require external Midtrans connectivity for the ordinary unit-test suite.

---

# 87. Frontend Tests

Dashboard should test important workflows such as:

```text
login
application creation
payment listing
payment details
gateway configuration
delivery retry
```

Use lightweight testing appropriate for React + Vite.

---

# 88. Linting and Code Quality

Backend:

```text
gofmt
go vet
golangci-lint
go test
```

Frontend:

```text
ESLint
TypeScript typecheck
Prettier
frontend tests
```

CI should reject broken formatting/types/tests.

---

# 89. CI/CD

GitHub Actions should at minimum run:

```text
Backend lint
Backend tests
Frontend lint
Frontend typecheck
Frontend tests
Build backend
Build dashboard
Docker build
```

Never put Midtrans production credentials in repository CI.

---

# 90. Acceptance Scenario

PayMux V1 is functional when this works:

```text
1. Administrator deploys PayMux.

2. Administrator configures Midtrans Sandbox.

3. Administrator creates:
   Product A
   Product B
   Product C.

4. Each product receives an API key and webhook secret.

5. Product B calls:

   POST /api/v1/payments

6. PayMux creates the payment internally.

7. PayMux creates a corresponding Midtrans Snap transaction.

8. Midtrans returns a Snap token.

9. PayMux returns:
   payment ID
   Snap token
   redirect URL

10. Product B opens Snap.

11. Customer completes payment.

12. Midtrans sends its notification to PayMux.

13. PayMux verifies the notification.

14. PayMux identifies the transaction as Product B's payment.

15. PayMux safely updates its local payment state.

16. PayMux generates:
    payment.paid

17. PayMux queues a webhook delivery.

18. Product B receives the signed PayMux event.

19. Products A and C receive nothing.

20. If Product B is offline:
    PayMux retries.

21. Administrator can inspect the entire transaction and
    webhook history from the React dashboard.

22. Product B can subsequently query transaction state.

23. Where applicable, Product B can:
    cancel
    expire
    refund
    manage supported subscription operations
    through PayMux instead of communicating directly
    with Midtrans.
```

---

# 91. Critical Engineering Rules

The coding agent MUST follow these rules:

1. **Do not implement PayMux as merely a webhook forwarder.**

2. **PayMux owns the gateway integration layer.**

3. **Applications should create payments through PayMux.**

4. **Snap is a first-class V1 feature.**

5. **Do not artificially restrict Midtrans features.**

6. **Keep Midtrans-specific code inside the Midtrans adapter.**

7. **Never expose Midtrans Server Keys to applications or browsers.**

8. **Never store raw card credentials.**

9. **Payment creation must support idempotency.**

10. **Gateway webhook processing must support idempotency.**

11. **Webhook delivery must be asynchronous and durable.**

12. **Use database constraints/transactions to protect correctness.**

13. **Do not trust client-provided application ownership. Resolve it from authentication.**

14. **Do not expose internal errors.**

15. **Do not silently discard unknown gateway events.**

16. **Do not hard-code Midtrans behavior from assumptions. Consult current official Midtrans documentation during implementation.**

17. **Backend: Go.**

18. **Dashboard: React + TypeScript + Vite.**

19. **Database: PostgreSQL.**

20. **Keep the design capable of adding additional gateway adapters without rewriting the core payment domain.**

---

# 92. Implementation Order for AI Coding Agent

Implement in this order:

## Phase 1 — Foundation

```text
Go project
configuration
PostgreSQL
migrations
logging
HTTP server
health checks
Docker
React/Vite dashboard foundation
```

## Phase 2 — Authentication & Applications

```text
admin authentication
applications
API keys
webhook destinations
gateway configuration
```

## Phase 3 — Midtrans Adapter

```text
Midtrans client
Sandbox/Production support
Snap transaction creation
transaction lookup
notification verification
normalization
```

## Phase 4 — Payment Domain

```text
payment creation
application order mapping
internal IDs
idempotency
transaction persistence
state machine
```

## Phase 5 — Notifications

```text
/webhooks/midtrans
verification
deduplication
payment update
event generation
```

## Phase 6 — Webhook Delivery

```text
queue
worker
HMAC signing
retry
dead-letter handling
delivery history
manual retry
```

## Phase 7 — Midtrans Transaction Operations

```text
status synchronization
cancel
expire
Snap session operations
refund
partial refund
```

## Phase 8 — Extended Midtrans Features

```text
subscriptions
recurring
tokenization-compatible flows
advanced Snap parameters
merchant capability handling
```

## Phase 9 — Dashboard

```text
overview
applications
gateway settings
payments
payment details
events
deliveries
refunds
subscriptions
```

## Phase 10 — OSS Readiness

```text
OpenAPI
README
documentation
SECURITY.md
CONTRIBUTING.md
LICENSE
CI
integration tests
sample applications
```

---

# 93. Final Product Principle

The architecture should be:

```text
Application
     │
     │ PayMux API
     ▼
┌────────────────────────────────┐
│             PayMux             │
│                                │
│ Authentication                 │
│ Payment Domain                 │
│ Transaction State              │
│ Gateway Abstraction            │
│ Webhook/Event System           │
│ Delivery/Retry Infrastructure  │
└───────────────┬────────────────┘
                │
                │ Gateway Adapter
                ▼
          ┌─────────────┐
          │  Midtrans   │
          │             │
          │ Snap        │
          │ Core API    │
          │ Subscription│
          │ Webhooks    │
          └─────────────┘
```

V1 is:

> **Midtrans-first.**

The architecture is:

> **Gateway-agnostic.**

The long-term goal is:

> **One integration for applications, multiple payment gateways behind PayMux.**
