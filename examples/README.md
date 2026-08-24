# Examples

Worked integrations, kept deliberately small. An application needs to do two
things with PayMux, open a payment, and verify the event that comes back, 
and these show both with nothing else in the way.

## merchant-go

A miniature storefront in Go with no dependencies beyond the standard library.

```bash
PAYMUX_URL=http://localhost:8080 \
PAYMUX_API_KEY=pmx_test_… \
PAYMUX_WEBHOOK_SECRET=whsec_… \
go run ./examples/merchant-go
```

Both credentials come from the PayMux dashboard and are shown **once**: the API
key when you create it under an application, and the signing secret when you
add that application's webhook destination.

Then take a payment:

```bash
curl -X POST http://localhost:9911/checkout \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"INV-000123","amount":150000,"email":"john@example.com"}'
```

```json
{
  "payment_id": "pay_01K…",
  "snap_token": "…",
  "redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/…"
}
```

Point the destination for that application at
`http://127.0.0.1:9911/webhooks/paymux` and the example will log each event as
it lands. Local addresses are blocked by default, so set
`PAYMUX_ALLOW_PRIVATE_WEBHOOK_DESTINATIONS=true` on your development instance
first, and leave it off in production, where it disables the SSRF protection.

### What to copy

`paymux.go` is the part worth lifting into your own service:

- `Client.CreatePayment`: one POST, with an `Idempotency-Key` derived from
  your own order so a retry after a timeout returns the original payment
  instead of opening a second one
- `VerifyWebhook`: checks the signature over the **raw** body and rejects a
  delivery whose timestamp is outside a five-minute window, which is what stops
  a captured delivery being replayed later

`main.go` is the surrounding storefront, and exists to show where those two
calls sit in a real request flow.

### The three things integrations get wrong

1. **Re-encoding the body before verifying.** The signature covers the exact
   bytes PayMux sent. A JSON round trip that looks lossless still reorders keys
   and changes whitespace, and verification will fail.
2. **Treating deliveries as exactly-once.** PayMux retries until you answer
   `2xx`, so the same event can arrive more than once. Key your fulfilment on
   the event `id`.
3. **Doing the work before answering.** PayMux does not wait for your
   fulfilment. Acknowledge quickly and process afterwards, or a slow handler
   simply earns itself a retry.

### Verifying in another language

The signed string is `<timestamp>.<delivery-id>.<raw body>`, keyed by the
destination secret, as HMAC-SHA256 in lowercase hex behind a `v1=` prefix.

```javascript
import { createHmac, timingSafeEqual } from 'node:crypto';

export function verify(secret, headers, rawBody) {
  const timestamp = headers['paymux-timestamp'];
  const deliveryId = headers['paymux-delivery-id'];

  const skew = Math.abs(Date.now() / 1000 - Number(timestamp));
  if (!Number.isFinite(skew) || skew > 300) throw new Error('delivery is stale');

  const mac = createHmac('sha256', secret);
  mac.update(`${timestamp}.${deliveryId}.`);
  mac.update(rawBody); // the raw Buffer, never a re-serialised object
  const expected = Buffer.from(`v1=${mac.digest('hex')}`);
  const received = Buffer.from(headers['paymux-signature'] ?? '');

  if (expected.length !== received.length || !timingSafeEqual(expected, received)) {
    throw new Error('signature does not match');
  }
  return JSON.parse(rawBody.toString('utf8'));
}
```

In Express, reach for `express.raw({ type: 'application/json' })` on this route
so `rawBody` is the untouched buffer rather than a parsed object.
