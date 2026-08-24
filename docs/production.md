# Running PayMux in production

PayMux moves money and holds gateway credentials. This is what to get right
before it handles a real transaction, and what to watch afterwards.

---

## Before the first deploy

### Generate and protect the encryption key

```bash
openssl rand -hex 32
```

`PAYMUX_ENCRYPTION_KEY` seals gateway server keys and webhook signing secrets
at rest. It is the one secret with no recovery path:

- **Lose it** and every stored gateway credential and webhook secret becomes
  unreadable. PayMux will refuse to start rather than sign with an empty key.
  Recovery means re-entering each gateway's server key and rotating every
  destination secret.
- **Leak it** and an attacker with a database copy has your Midtrans server
  key. Rotate it, re-enter the credentials, and rotate the destination secrets.

Keep it in a secret manager, not in the deployment's `.env`. Back it up
separately from the database — a backup containing both is a backup that
protects nothing.

### Set these correctly

| Setting | Production value | Why |
|---|---|---|
| `PAYMUX_ENV` | `production` | Marks session cookies `Secure` and switches logs to JSON |
| `PAYMUX_TRUST_PROXY_HEADERS` | `true` behind a proxy, else `false` | Left off behind a proxy, every request looks like it came from the proxy: rate limits become one global bucket and the audit trail records the wrong address. Turned on **without** a proxy, any caller can forge their own address and evade rate limits |
| `PAYMUX_CORS_ORIGINS` | the dashboard's exact origin | A wildcard is ignored, because admin responses carry credentials |
| `PAYMUX_ALLOW_PRIVATE_WEBHOOK_DESTINATIONS` | `false` | `true` disables SSRF protection on webhook destinations |
| `PAYMUX_ADMIN_EMAIL` / `PASSWORD` | **remove after first start** | Only used to seed the first administrator |
| `PAYMUX_RATE_LIMIT_PER_SECOND` | tune to your traffic | Per API key, not per address |

### Terminate TLS in front of PayMux

PayMux serves plain HTTP and expects a reverse proxy. Gateway notifications and
API keys travel over that connection, so it must be TLS.

The proxy must overwrite `X-Forwarded-For` rather than append to a
client-supplied value — otherwise `PAYMUX_TRUST_PROXY_HEADERS=true` trusts
whatever the caller wrote. Add HSTS at the proxy; PayMux does not set it,
because only the edge knows whether every subdomain is HTTPS.

Do not expose these publicly:

- `/metrics` on the API, and the worker's metrics port — unauthenticated by
  design, for a scraper on a private network
- PostgreSQL — the bundled compose file no longer publishes it

### Point the gateway at PayMux

In the Midtrans dashboard set the Payment Notification URL to
`https://your-host/webhooks/midtrans`, then press **Test connection** in
PayMux's Gateways screen to confirm the credentials before taking a payment.

---

## Scaling

The API is stateless — run as many as you like behind the load balancer. No
transaction state lives in process memory.

The worker can also run several instances. They coordinate through the database
with `FOR UPDATE SKIP LOCKED`, so each delivery is claimed by exactly one
worker. Size the database pool for it: each in-flight delivery needs a
connection, and the worker already requests `PAYMUX_WORKER_CONCURRENCY + 2`.

Migrations run on startup under an advisory lock, so simultaneous starts are
safe. A rolling deploy where an old and new version overlap is only safe if the
new migration is backward-compatible with the old code — add columns, don't
rename them.

---

## Backups

Back up PostgreSQL and the encryption key, separately.

```bash
docker compose exec -T postgres pg_dump -U paymux --format=custom paymux > paymux-$(date +%F).dump
```

Test a restore before you need one. A dump you have never restored is a
hypothesis, not a backup.

What a lost database costs you: payment history, the audit trail, and the
idempotency records that make retries safe. PayMux can re-derive a payment's
current state from the gateway with **Sync**, but not its history.

---

## Data growth

Five tables grow without bound, and nothing prunes them today. At low volume
this is fine for years; plan for it before it is not.

| Table | Grows with |
|---|---|
| `events` | one row per payment state change |
| `deliveries` | one row per event per destination |
| `delivery_attempts` | one row per attempt — up to 7 per delivery |
| `gateway_events` | one row per notification the gateway sends |
| `audit_logs` | one row per administrative action |
| `payout_transitions` | one row per payout state change — keep these |

Expired sessions and idempotency keys *are* pruned hourly by the worker.

If you need retention, delete oldest-first and keep payments — they are the
record you are legally likeliest to need:

```sql
-- Keep 90 days of delivery history. Attempts cascade with their delivery.
DELETE FROM deliveries
WHERE created_at < now() - interval '90 days'
  AND state IN ('succeeded', 'dead', 'canceled');

DELETE FROM gateway_events WHERE received_at < now() - interval '90 days';
```

Run it during a quiet period; a large first delete will hold locks.

---

## What to watch

PayMux exports Prometheus metrics from both the API and the worker.

**Alert on these:**

| Signal | Query | Means |
|---|---|---|
| Deliveries dying | `increase(paymux_delivery_failures_total[15m])` rising | An application's endpoint is down or rejecting |
| Unroutable notifications | `paymux_webhook_received_total{routing="unrouted"}` increasing | The gateway is reporting orders PayMux did not create — check whether something else shares the merchant account |
| Rejected notifications | `paymux_webhook_received_total{routing="rejected"}` above zero | Signature failures: wrong server key configured, or someone is probing you |
| Gateway errors | `paymux_gateway_requests_total{outcome="error"}` rising | Credentials expired, or the gateway is down |
| Queue backing up | `paymux_delivery_queue_depth{state="failed"}` climbing | Workers cannot keep up, or destinations are failing |

A `routing="rejected"` count that climbs steadily is worth investigating
promptly. It means either a misconfiguration that is silently dropping real
payment notifications, or someone attempting to forge them.

**Also watch:** `/ready` on the API — it reports unready when PostgreSQL is
unreachable, which is what should pull an instance out of the load balancer.

---

## Incident playbook

**An application stopped receiving events.** Check Deliveries filtered to
`failed` and `dead`. The last status code and error are recorded per attempt.
Once their endpoint is healthy, use **Retry** — it restores the full attempt
budget rather than granting one last try.

**A payment looks wrong.** Open it and read the trace: it shows the gateway's
notification, what PayMux normalized it to, the event published, and each
delivery attempt with its response. **Sync with gateway** re-reads the
authoritative state.

**Notifications are being rejected.** The signature is computed from the
server key. Confirm the key in Gateways matches the environment — a sandbox
key against production credentials fails exactly this way. **Test connection**
settles it in one click.

**A key or secret leaked.** Revoke the API key in Applications; it stops
working immediately. Rotate a destination secret in place — deliveries already
queued are signed with whichever secret is current when they are sent, so have
the receiver accept both during the change.

**PayMux will not start after a config change.** It refuses rather than running
degraded. The message names the setting. A sealed credential that cannot be
decrypted means `PAYMUX_ENCRYPTION_KEY` changed.

---

## Upgrading

1. Read the release notes for migrations.
2. Back up the database.
3. Deploy the API first — it applies migrations under an advisory lock.
4. Deploy the worker.

Migrations are forward-only and never edited once applied; PayMux verifies a
checksum and refuses to start if an applied migration changed underneath it.
There is no automatic rollback: to go back, restore the backup.


---

## Disbursement

Everything else in PayMux moves money inward, where the worst case of a
mistake is a payment that gets refunded through the gateway that took it. A
payout has no such backstop. Read this section before turning it on.

### It is off in three places, on purpose

A payout only happens when all three are true:

1. The **gateway account** holds a disbursement creator key. Midtrans issues
   these separately from the payment server key, and only after approving your
   account for disbursement.
2. The **application** has payouts enabled, in Applications → the application →
   Paying money out.
3. Somebody **approves** the payout, unless you have turned approval off — and
   PayMux refuses to let you turn it off without setting a limit.

Turning on one without the others does nothing. That is the intent: money
leaving needs more than one decision behind it.

### Why the per-application limit matters more than it looks

PayMux exists so several applications can share one merchant account. On the
way in that is harmless — application A cannot take application B's money. On
the way out, every application with an API key spends from the *same balance*.

So the limits are not a nicety. Without them, one compromised API key drains
what every other application collected. Set `max_amount` and `daily_limit` for
every application you enable, and size them to what that application
legitimately pays out in a day, not to what the balance holds.

The daily limit counts payouts that are still in flight, including ones whose
outcome is unknown. Money the gateway already has cannot be spent twice.

### The two keys are not interchangeable

Midtrans issues a **creator** key and an **approver** key so that whoever can
request a payout cannot also release it. PayMux keeps them apart and seals them
separately.

Give PayMux the approver key only if you want payouts released from PayMux. If
you leave it out, PayMux can request payouts but they wait in the Midtrans
dashboard for someone to release them there — which is a legitimate and
stricter setup, not a broken one.

### UNRESOLVED is the state that matters

Most payout states are self-explanatory. One is not:

> **UNRESOLVED** — PayMux sent the payout and does not know what happened.
> The connection failed at the one moment where failure is ambiguous. The money
> may or may not be moving.

PayMux resolves these by itself, by re-sending under the *original* idempotency
key. Midtrans answers that with the original result rather than making a second
transfer. This works for 20 hours, after which Midtrans forgets the key.

**Do not re-issue a payout that is UNRESOLVED.** A new payout means a new key,
and a new key means Midtrans treats it as a new instruction. If the first one
did go out, you have now paid twice, and there is no chargeback on a
disbursement.

If a payout is still UNRESOLVED after the window closes, PayMux stops trying
and logs it at ERROR. That one needs a person: check the Midtrans dashboard for
the reference, decide what actually happened, and settle it there.

### Watch these

| Signal | Query | Means |
|---|---|---|
| Unknown outcomes | `paymux_payouts_total{status="unresolved"}` above zero | Money may have moved without PayMux knowing. Investigate each one |
| Payouts failing | `increase(paymux_payouts_total{status="failed"}[15m])` rising | Bad credentials, bad beneficiary details, or an empty balance |
| Approval queue growing | payouts in REQUESTED for hours | Nobody is releasing them; somebody is not getting paid |

Also grep the API and worker logs for `payout outcome is unknown` and
`payout outcome can no longer be resolved automatically`. Both are ERROR level
and both mean somebody's money is unaccounted for.

### Backups

The `payouts` and `payout_transitions` tables are the record of who authorised
moving money and when. A payment can be re-derived from the gateway; the answer
to "who approved this" exists only in PayMux. Do not prune them.

---

## What PayMux does not do

Know these limits before you rely on it:

- **No automatic reconciliation.** If a notification is never delivered by the
  gateway, the payment stays in its last known state until something calls
  **Sync**. There is no background sweep.
- **No retention or archival policy** — see Data growth above.
- **No multi-region or active-active story.** One PostgreSQL primary.
- **Administrator accounts have no roles.** Every administrator can do
  everything, including reading every application's payments and approving
  payouts. The only separation PayMux enforces is that an approver cannot
  approve their own request.
- **No 2FA on the dashboard.** Protect it at the network edge if that matters.
- **Recurring billing depends on gateway activation.** PayMux implements the
  lifecycle; Midtrans decides whether your account may use it.
