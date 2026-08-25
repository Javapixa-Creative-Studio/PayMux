# Examples

Worked integrations, kept deliberately small. An application needs to do two
things with PayMux, open a payment, and verify the event that comes back, 
and these show both with nothing else in the way.

## demo-shop

Two browsable storefronts, to make PayMux's premise visible rather than
described. Kopi Rakyat sells coffee, Margin Notes sells books, and both collect
into one Midtrans account while neither can see the other's orders.

```bash
docker compose --profile demo up -d
```

Then open [localhost:9911](http://localhost:9911) and
[localhost:9921](http://localhost:9921), buy something, and pay it in the
Midtrans sandbox. The order moves to `paid` on the shop's own Orders page when
the signed webhook arrives, not when the browser comes back, which is the
distinction most integrations blur.

It needs four values in your `.env`, one API key and one webhook secret per
shop, each created in the PayMux dashboard under its own application:

```
SHOP_A_API_KEY=pmx_test_…
SHOP_A_WEBHOOK_SECRET=whsec_…
SHOP_B_API_KEY=pmx_test_…
SHOP_B_WEBHOOK_SECRET=whsec_…
```

Point each application's destination at `http://host.docker.internal:9911` or
`:9921` respectively. From inside a container, `127.0.0.1` is the container's
own loopback rather than your machine, which is the single most common reason
a local webhook never arrives.

It is one binary run twice. The catalogue, name and colour come from the
environment, so the difference you see between the two shops is exactly the
difference PayMux sees: two API keys.

### Dialog or redirect

`SHOP_CHECKOUT` picks how a shop takes payment, and the two demo shops are
deliberately set differently so both are visible:

- `popup` shows the gateway's checkout in a dialog and keeps the customer on
  the page. Kopi Rakyat does this.
- `redirect` sends them to the gateway and back, which needs no JavaScript.
  Margin Notes does this.

A shop asking for `popup` needs two things in the browser: the checkout script
and the merchant's client key. It does not configure either. It asks PayMux at
startup:

```bash
curl -s "$PAYMUX_URL/api/v1/gateway/capabilities" -H "Authorization: Bearer $PAYMUX_KEY"
```

```json
{
  "gateway": "midtrans",
  "environment": "sandbox",
  "client_key": "SB-Mid-client-…",
  "checkout_script_url": "https://app.sandbox.midtrans.com/snap/snap.js"
}
```

Which gateway is behind PayMux and which environment it points at is the
merchant's configuration, so an application that hardcoded those hostnames
would break the day the merchant moved to production. The client key is safe
to put in a page: it names the merchant to the script and authorises nothing.

The Buy control stays a real form even in `popup` mode, and the script only
intercepts its submit. A browser that never runs the script still checks out,
by redirecting.

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

The `paymuxgo` package is the part worth lifting into your own service, and
both examples here use it unchanged:

- `Client.CreatePayment`: one POST, with an `Idempotency-Key` derived from
  your own order so a retry after a timeout returns the original payment
  instead of opening a second one
- `VerifyWebhook`: checks the signature over the **raw** body and rejects a
  delivery whose timestamp is outside a five-minute window, which is what stops
  a captured delivery being replayed later

`merchant-go/main.go` is the surrounding storefront, and exists to show where
those two calls sit in a real request flow.

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
