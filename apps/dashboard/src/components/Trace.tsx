/**
 * The payment trace: a payment's chain of custody, in order.
 *
 * Every other view answers "what state is this payment in". This one answers
 * the question an operator actually opens the dashboard with: *where did it
 * stop?* It puts the gateway's notification, PayMux's normalized state, the
 * event it published and each delivery attempt on one spine, so a break in the
 * chain is visible at a glance rather than reconstructed from four tables.
 */

import { Link } from 'react-router-dom';

import { formatTransportError } from '../lib/format';

import type { Delivery, GatewayEvent, Payment, PayMuxEvent, Refund } from '../api/types';
import { Id, Timestamp } from './primitives';

type Tone = 'settled' | 'pending' | 'failed' | 'inert';

interface TraceEntry {
  at: string;
  actor: string;
  headline: string;
  detail?: React.ReactNode;
  meta?: string[];
  tone: Tone;
  key: string;
}

interface TraceProps {
  payment: Payment;
  events: PayMuxEvent[];
  deliveries: Delivery[];
  gatewayEvents: GatewayEvent[];
  refunds: Refund[];
}

export function Trace({ payment, events, deliveries, gatewayEvents, refunds }: TraceProps) {
  const entries: TraceEntry[] = [];

  // The payment.created event covers the same instant with more detail, so
  // the synthetic opening entry only appears when that event is absent,
  // otherwise the trace would open by saying the same thing twice.
  const created = events.find((event) => event.type === 'payment.created');
  if (!created) {
    entries.push({
      key: `created-${payment.id}`,
      at: payment.created_at,
      actor: 'paymux',
      headline: 'Payment created',
      detail: payment.snap_token
        ? 'Checkout session opened; token issued to the application.'
        : 'Recorded before the gateway was called.',
      meta: [`order ${payment.application_order_id}`],
      tone: 'inert',
    });
  }

  // What the gateway told PayMux, and whether it was believed.
  for (const notification of gatewayEvents) {
    const verified = notification.signature_verified;
    entries.push({
      key: `gw-${notification.id}`,
      at: notification.received_at,
      actor: notification.gateway,
      headline: verified ? 'Notification received' : 'Notification rejected',
      detail: verified
        ? describeNotification(notification)
        : 'The signature did not verify. Nothing was applied to this payment.',
      meta: [
        verified ? 'signature ok' : 'signature failed',
        `routing ${notification.routing_status}`,
        ...(notification.routing_error ? [notification.routing_error] : []),
      ],
      tone: verified ? routingTone(notification.routing_status) : 'failed',
    });
  }

  // What PayMux published as a result.
  for (const event of events) {
    const opening = event.type === 'payment.created';
    entries.push({
      key: `evt-${event.id}`,
      at: event.created_at,
      actor: 'paymux',
      headline: event.type,
      detail: opening
        ? payment.snap_token
          ? 'Checkout session opened; token issued to the application.'
          : 'Payment opened at the gateway.'
        : event.data?.status
          ? `Payment state is now ${event.data.status}.`
          : undefined,
      meta: [`event ${event.id}`, ...(opening ? [`order ${payment.application_order_id}`] : [])],
      tone: eventTone(event.type),
    });
  }

  for (const refund of refunds) {
    entries.push({
      key: `rfd-${refund.id}`,
      at: refund.created_at,
      actor: 'paymux',
      headline: refund.status === 'FAILED' ? 'Refund failed' : 'Refund recorded',
      detail: refund.failure_reason || refund.reason || undefined,
      meta: [`refund ${refund.id}`],
      tone: refund.status === 'SUCCEEDED' ? 'settled' : refund.status === 'FAILED' ? 'failed' : 'pending',
    });
  }

  // Whether the owning application actually heard about it.
  for (const delivery of deliveries) {
    entries.push({
      key: `dlv-${delivery.id}`,
      at: delivery.last_attempt_at || delivery.created_at,
      actor: 'delivery',
      headline: describeDelivery(delivery),
      detail: <span className="mono">{delivery.url}</span>,
      meta: [
        ...(delivery.last_status_code ? [`HTTP ${delivery.last_status_code}`] : []),
        ...(delivery.last_duration_ms != null ? [`${delivery.last_duration_ms}ms`] : []),
        `attempt ${delivery.attempt_count}/${delivery.max_attempts}`,
        ...(delivery.last_error ? [formatTransportError(delivery.last_error)] : []),
      ],
      tone: deliveryTone(delivery),
    });
  }

  entries.sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());

  if (entries.length === 0) {
    return <p className="empty__hint">Nothing has happened to this payment yet.</p>;
  }

  return (
    <ol className="trace">
      {entries.map((entry) => (
        <li className="trace__entry" key={entry.key}>
          <div className="trace__time">
            <Timestamp value={entry.at} absolute />
          </div>
          <div className="trace__spine">
            <span className={`trace__node trace__node--${entry.tone}`} aria-hidden="true" />
          </div>
          <div className="trace__body">
            <div className="trace__headline">
              <span>{entry.headline}</span>
              <span className="trace__actor">{entry.actor}</span>
            </div>
            {entry.detail && <div className="trace__detail">{entry.detail}</div>}
            {entry.meta && entry.meta.length > 0 && (
              <div className="trace__meta">
                {entry.meta.map((item) => (
                  <span key={item}>{item}</span>
                ))}
              </div>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}

function describeNotification(notification: GatewayEvent): string {
  const parts = [notification.gateway_status || 'unknown status'];
  if (notification.fraud_status) parts.push(`fraud ${notification.fraud_status}`);
  return parts.join(' · ');
}

function describeDelivery(delivery: Delivery): string {
  switch (delivery.state) {
    case 'succeeded':
      return 'Delivered to the application';
    case 'dead':
      return 'Delivery gave up';
    case 'failed':
      return 'Delivery failed, retry scheduled';
    case 'delivering':
      return 'Delivering now';
    case 'canceled':
      return 'Delivery canceled';
    default:
      return 'Delivery queued';
  }
}

function routingTone(status: GatewayEvent['routing_status']): Tone {
  switch (status) {
    case 'routed':
      return 'settled';
    case 'unrouted':
    case 'rejected':
      return 'failed';
    default:
      return 'inert';
  }
}

function eventTone(type: string): Tone {
  if (type.endsWith('.paid') || type.endsWith('.completed') || type.endsWith('.refunded')) {
    return 'settled';
  }
  if (type.endsWith('.failed') || type.endsWith('.canceled') || type.endsWith('.expired')) {
    return 'failed';
  }
  if (type.endsWith('.pending') || type.endsWith('.authorized')) {
    return 'pending';
  }
  return 'inert';
}

function deliveryTone(delivery: Delivery): Tone {
  switch (delivery.state) {
    case 'succeeded':
      return 'settled';
    case 'dead':
    case 'failed':
      return 'failed';
    case 'pending':
    case 'delivering':
      return 'pending';
    default:
      return 'inert';
  }
}

/** A compact link to a delivery, used from the trace's neighbouring tables. */
export function DeliveryLink({ id }: { id: string }) {
  return (
    <Link to={`/deliveries?delivery=${id}`}>
      <Id value={id} />
    </Link>
  );
}
