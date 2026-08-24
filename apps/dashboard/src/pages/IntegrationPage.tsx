import { Link } from 'react-router-dom';

import { API_BASE_URL } from '../api/client';
import { useApplications } from '../api/queries';
import { Snippet } from '../components/Snippet';

/**
 * How to build against this PayMux instance.
 *
 * Written for the person integrating an application, not for an operator, and
 * kept in the console rather than in a README because the two things a
 * developer needs — the endpoints and their own API key — are both here. It
 * uses this instance's real host so a command can be copied rather than
 * adapted, and it is ordered the way an integration actually happens: get a
 * key, take a payment, hear about it, then pay somebody.
 */
export function IntegrationPage() {
  const applications = useApplications();

  // The API answers on its own origin. In development the dashboard is served
  // by Vite on another port, so the examples have to name the API's, not the
  // page's, or every copied command would go to the wrong place.
  const apiBase = API_BASE_URL || window.location.origin.replace(':5173', ':8080');
  const first = applications.data?.data?.[0];

  return (
    <>
      <div className="page__head">
        <h1>Integration</h1>
      </div>
      <p className="page__lede">
        Everything an application needs to talk to this PayMux. The examples use this instance's own
        address, so they can be copied as they are — only the API key is yours to fill in.
      </p>

      <nav className="docnav" aria-label="Sections">
        {[
          ['auth', 'Authenticate'],
          ['payments', 'Take a payment'],
          ['webhooks', 'Receive events'],
          ['payouts', 'Pay money out'],
          ['errors', 'Errors'],
        ].map(([id, label]) => (
          <a key={id} href={`#${id}`} className="docnav__link">
            {label}
          </a>
        ))}
      </nav>

      {/* ---------------------------------------------------------------- */}

      <section className="doc" id="auth">
        <h2>Authenticate</h2>
        <p>
          Every call carries an API key as a bearer token. Create one under{' '}
          <Link to={first ? `/applications/${first.id}` : '/applications'}>Applications</Link> — it
          is shown once, at creation, and never again.
        </p>

        <Snippet
          label="shell"
          code={`export PAYMUX_KEY='pmx_test_…'\nexport PAYMUX_URL='${apiBase}'`}
        />

        <div className="doc__note">
          <strong>Test keys and live keys are not interchangeable.</strong> A key's mode has to match
          the gateway account's environment, and PayMux refuses the call rather than quietly
          charging the wrong account. If you see{' '}
          <em>"This API key's mode does not match the configured gateway environment"</em>, that is
          the guard working.
        </div>

        <p>
          Ask what this instance's gateway supports before assuming a feature exists — subscriptions
          and disbursement both depend on the merchant account, not on PayMux:
        </p>
        <Snippet
          label="shell"
          code={`curl -s "$PAYMUX_URL/api/v1/gateway/capabilities" \\\n  -H "Authorization: Bearer $PAYMUX_KEY"`}
        />
      </section>

      {/* ---------------------------------------------------------------- */}

      <section className="doc" id="payments">
        <h2>Take a payment</h2>
        <p>
          One call opens a checkout session. PayMux returns a URL to send the customer to; you never
          handle card details.
        </p>

        <Snippet
          label="shell"
          code={`curl -s -X POST "$PAYMUX_URL/api/v1/payments" \\
  -H "Authorization: Bearer $PAYMUX_KEY" \\
  -H 'Content-Type: application/json' \\
  -H "Idempotency-Key: order-1001" \\
  -d '{
    "application_order_id": "INV-1001",
    "amount": 150000,
    "currency": "IDR",
    "customer": { "first_name": "Ada", "email": "ada@example.com" },
    "items": [{ "name": "One widget", "price": 150000, "quantity": 1 }]
  }'`}
        />

        <ul className="doc__list">
          <li>
            <strong>amount</strong> is in minor units. For IDR that is whole rupiah, so{' '}
            <code>150000</code> is Rp 150.000. PayMux never uses floating point for money and
            neither should you.
          </li>
          <li>
            <strong>application_order_id</strong> is your own reference and must be unique within
            your application. It is how you find the payment again.
          </li>
          <li>
            <strong>Idempotency-Key</strong> makes a retry safe. Send the same key with the same body
            and you get the original payment back instead of a second one.
          </li>
        </ul>

        <p>Send the customer to the returned URL:</p>
        <Snippet
          label="json"
          code={`{
  "id": "pay_01ARZ3NDEKTSV4RRFFQ69G5FAV",
  "status": "PENDING",
  "amount": 150000,
  "currency": "IDR",
  "redirect_url": "https://app.sandbox.midtrans.com/snap/v4/redirection/…",
  "snap_token": "…"
}`}
        />

        <p>
          Read it back with <code>GET /api/v1/payments/{'{id}'}</code>, and if you ever doubt what
          PayMux holds, <code>POST /api/v1/payments/{'{id}'}/sync</code> re-reads the authoritative
          state from the gateway. Refunds are{' '}
          <code>POST /api/v1/payments/{'{id}'}/refunds</code>.
        </p>
      </section>

      {/* ---------------------------------------------------------------- */}

      <section className="doc" id="webhooks">
        <h2>Receive events</h2>
        <p>
          Do not poll. Add a destination under your application and PayMux delivers every state
          change to it, signed, with retries and a replayable history.
        </p>

        <div className="doc__note">
          <strong>Verify the signature on every delivery.</strong> The URL is the only thing
          separating a real event from anyone who guesses it. A payment marked paid because an
          unsigned request said so is a free order.
        </div>

        <p>Each delivery carries these headers:</p>
        <Snippet
          label="http"
          code={`PayMux-Event-Id:     evt_01ARZ3NDEKTSV4RRFFQ69G5FAV
PayMux-Delivery-Id:  dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV
PayMux-Event-Type:   payment.paid
PayMux-Timestamp:    1750000000
PayMux-Attempt:      1
PayMux-Signature:    v1=<hex hmac-sha256>`}
        />

        <p>
          The signature is HMAC-SHA256 over{' '}
          <code>{'<timestamp>.<delivery id>.<raw body>'}</code>, keyed with the destination's
          signing secret. Sign the <em>raw</em> body — re-serialising the JSON first will change a
          byte somewhere and the signature will not match.
        </p>

        <Snippet
          label="node"
          code={`import crypto from 'node:crypto';

// Give express the raw body: express.raw({ type: 'application/json' })
app.post('/webhooks/paymux', (req, res) => {
  const ts  = req.header('PayMux-Timestamp');
  const id  = req.header('PayMux-Delivery-Id');
  const sig = req.header('PayMux-Signature') ?? '';

  const expected = 'v1=' + crypto
    .createHmac('sha256', process.env.PAYMUX_WEBHOOK_SECRET)
    .update(\`\${ts}.\${id}.\`)
    .update(req.body)            // the raw Buffer, not a re-encoded object
    .digest('hex');

  const ok = sig.split(',').some((candidate) => {
    const a = Buffer.from(candidate.trim());
    const b = Buffer.from(expected);
    return a.length === b.length && crypto.timingSafeEqual(a, b);
  });
  if (!ok) return res.sendStatus(401);

  const event = JSON.parse(req.body);
  // Do your work, then answer 2xx. Anything else is retried.
  res.sendStatus(200);
});`}
        />

        <ul className="doc__list">
          <li>
            <strong>Answer 2xx quickly.</strong> Anything else is retried with backoff, up to seven
            attempts, and then parked as <code>dead</code> for you to replay from{' '}
            <Link to="/deliveries">Deliveries</Link>.
          </li>
          <li>
            <strong>Expect duplicates.</strong> A delivery that timed out after your handler
            committed will arrive again. Key your side on <code>PayMux-Event-Id</code>.
          </li>
          <li>
            <strong>Several signatures may be listed</strong>, comma-separated, while a secret is
            being rotated. Accept the delivery if any one of them matches.
          </li>
        </ul>

        <p>
          A complete worked receiver lives in <code>examples/merchant-go</code> in the repository,
          with tests that verify against PayMux's own signing code so the two cannot drift.
        </p>
      </section>

      {/* ---------------------------------------------------------------- */}

      <section className="doc" id="payouts">
        <h2>Pay money out</h2>

        <div className="doc__note doc__note--warn">
          <strong>This is off until somebody turns it on.</strong> An application needs payouts
          enabled and a limit set under Applications → your application → <em>Paying money out</em>,
          and the gateway account needs disbursement credentials. Holding a valid API key is not
          enough, on purpose: every application shares one merchant balance.
        </div>

        <p>
          A payout goes to a <strong>beneficiary</strong>, never to a raw account number in the
          request. A destination is reviewed once and reused, so a typo cannot send money to a
          stranger on a Tuesday afternoon.
        </p>

        <Snippet
          label="shell"
          code={`# 1. Register the destination once.
curl -s -X POST "$PAYMUX_URL/api/v1/beneficiaries" \\
  -H "Authorization: Bearer $PAYMUX_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{
    "alias":   "supplier-a",
    "name":    "PT Supplier A",
    "account": "1172993826",
    "bank":    "bni"
  }'

# 2. Ask the bank who owns it, before any money is involved.
curl -s -X POST "$PAYMUX_URL/api/v1/beneficiaries/{id}/verify" \\
  -H "Authorization: Bearer $PAYMUX_KEY"`}
        />

        <p>
          Valid bank codes come from the gateway, not from PayMux —{' '}
          <code>GET /api/v1/payout-banks</code> lists them. Use <code>gopay</code> or{' '}
          <code>ovo</code> with a phone number to pay an e-wallet.
        </p>

        <Snippet
          label="shell"
          code={`curl -s -X POST "$PAYMUX_URL/api/v1/payouts" \\
  -H "Authorization: Bearer $PAYMUX_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{
    "application_payout_id": "PO-2026-0042",
    "beneficiary_alias":     "supplier-a",
    "amount":                250000,
    "notes":                 "Invoice 42"
  }'`}
        />

        <ul className="doc__list">
          <li>
            <strong>202 means accepted, not sent.</strong> A payout awaiting approval has moved
            nothing. You get 201 only when the application is configured to skip approval.
          </li>
          <li>
            <strong>application_payout_id</strong> is the safety mechanism. Reuse it and you get the
            original payout back — a retried request can never become a second transfer.
          </li>
          <li>
            Watch for <code>payout.completed</code> and <code>payout.failed</code> on your webhook.
            The failure carries the gateway's own reason.
          </li>
        </ul>

        <div className="doc__note doc__note--warn">
          <strong>If a payout is UNRESOLVED, do not create a replacement.</strong> It means PayMux
          sent it and cannot yet tell what happened. PayMux resolves it by re-asking under the
          original idempotency key, which returns the original result instead of transferring again.
          A new payout would carry a new key, and the gateway would treat it as a second
          instruction. There is no chargeback on a disbursement.
        </div>
      </section>

      {/* ---------------------------------------------------------------- */}

      <section className="doc" id="errors">
        <h2>Errors</h2>
        <p>Every failure has the same shape, and the code is stable enough to branch on.</p>

        <Snippet
          label="json"
          code={`{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "The request contains invalid values.",
    "fields": [{ "field": "amount", "message": "must be greater than zero" }],
    "request_id": "req_01ARZ3NDEKTSV4RRFFQ69G5FAV"
  }
}`}
        />

        <p>
          Quote the <code>request_id</code> when something needs explaining — it appears in the
          server logs for that exact call.
        </p>

        <div className="tablewrap">
          <table>
            <thead>
              <tr>
                <th>Status</th>
                <th>Means</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td data-label="Status" data-primary="" className="mono">
                  401 / 403
                </td>
                <td data-label="Means">
                  The key is wrong, revoked, in the wrong mode, or not permitted to do this.
                </td>
              </tr>
              <tr>
                <td data-label="Status" data-primary="" className="mono">
                  409
                </td>
                <td data-label="Means">
                  A duplicate reference, or an idempotency key reused with a different body.
                </td>
              </tr>
              <tr>
                <td data-label="Status" data-primary="" className="mono">
                  412
                </td>
                <td data-label="Means">
                  Something is not configured yet — no gateway account, or no disbursement
                  credentials.
                </td>
              </tr>
              <tr>
                <td data-label="Status" data-primary="" className="mono">
                  422
                </td>
                <td data-label="Means">
                  The request was understood and refused: over a limit, or the gateway declined it.
                </td>
              </tr>
              <tr>
                <td data-label="Status" data-primary="" className="mono">
                  429
                </td>
                <td data-label="Means">
                  Rate limited. The budget is per API key, so slow down rather than rotating keys.
                </td>
              </tr>
              <tr>
                <td data-label="Status" data-primary="" className="mono">
                  5xx
                </td>
                <td data-label="Means">
                  PayMux's problem. Safe to retry — with the same Idempotency-Key.
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
