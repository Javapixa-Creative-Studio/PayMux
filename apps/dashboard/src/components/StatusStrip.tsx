/**
 * A single-line instrument readout, present on every page.
 *
 * The operator's question does not change as they move between screens: did
 * the money move, and did the applications hear about it, so the answer stays
 * on screen rather than living on a dashboard page they have to navigate back
 * to. It is one dense row, not a grid of cards: this is a status bar, and it
 * should read like one.
 */

import { useOverview } from '../api/queries';

type Tone = 'settled' | 'pending' | 'failed' | 'quiet' | undefined;

function Metric({ label, value, tone }: { label: string; value: string | number; tone?: Tone }) {
  return (
    <div className="strip__metric">
      <span className={tone ? `strip__value strip__value--${tone}` : 'strip__value'}>{value}</span>
      <span className="strip__label">{label}</span>
    </div>
  );
}

export function StatusStrip() {
  const { data, isPending, isError } = useOverview('24h');

  if (isPending) {
    return (
      <div className="strip">
        <span className="strip__window">last 24h</span>
        <div className="strip__metric">
          <span className="skeleton" style={{ width: 180 }} />
        </div>
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="strip">
        <span className="strip__window">last 24h</span>
        <Metric label="metrics unavailable" value="—" tone="failed" />
      </div>
    );
  }

  const failedDeliveries = data.deliveries.failed + data.deliveries.dead;

  return (
    <div className="strip">
      <span className="strip__window">last 24h</span>

      <Metric label="payments" value={data.payments.created} />
      <Metric label="paid" value={data.payments.paid} tone={data.payments.paid > 0 ? 'settled' : 'quiet'} />
      <Metric
        label="pending"
        value={data.payments.pending}
        tone={data.payments.pending > 0 ? 'pending' : 'quiet'}
      />
      <Metric
        label="unsuccessful"
        value={data.payments.failed}
        tone={data.payments.failed > 0 ? 'failed' : 'quiet'}
      />

      <Metric label="delivered" value={data.deliveries.succeeded} tone="quiet" />
      <Metric
        label="deliveries failing"
        value={failedDeliveries}
        tone={failedDeliveries > 0 ? 'failed' : 'quiet'}
      />
      <Metric
        label="unrouted"
        value={data.unrouted_notifications}
        tone={data.unrouted_notifications > 0 ? 'failed' : 'quiet'}
      />

      {data.currency_totals?.map((total) => (
        <Metric
          key={total.currency}
          label={`${total.currency} settled`}
          value={new Intl.NumberFormat('en-US').format(
            total.currency === 'IDR' ? total.paid_total : total.paid_total / 100,
          )}
          tone="settled"
        />
      ))}
    </div>
  );
}
