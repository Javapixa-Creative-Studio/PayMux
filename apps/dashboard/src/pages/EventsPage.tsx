import { useState } from 'react';
import { Link } from 'react-router-dom';

import { useEvents } from '../api/queries';
import { Modal } from '../components/Modal';
import {
  CodeBlock,
  Empty,
  ErrorNotice,
  Id,
  Loading,
  Tag,
  Timestamp,
} from '../components/primitives';
import type { PayMuxEvent } from '../api/types';

const TYPES = [
  'payment.created',
  'payment.pending',
  'payment.authorized',
  'payment.paid',
  'payment.failed',
  'payment.canceled',
  'payment.expired',
  'payment.refunded',
  'payment.partially_refunded',
  'refund.created',
  'refund.completed',
  'refund.failed',
  'subscription.created',
  'subscription.updated',
  'subscription.enabled',
  'subscription.disabled',
  'subscription.canceled',
];

function tone(type: string): 'settled' | 'pending' | 'failed' | 'inert' {
  if (type.endsWith('.paid') || type.endsWith('.completed') || type.endsWith('.refunded')) {
    return 'settled';
  }
  if (type.endsWith('.failed') || type.endsWith('.canceled') || type.endsWith('.expired')) {
    return 'failed';
  }
  if (type.endsWith('.pending') || type.endsWith('.authorized')) return 'pending';
  return 'inert';
}

export function EventsPage() {
  const [type, setType] = useState('');
  const [inspecting, setInspecting] = useState<PayMuxEvent | null>(null);
  const events = useEvents({ type: type || undefined, limit: 50 });

  return (
    <>
      <div className="page__head">
        <h1>Events</h1>
      </div>
      <p className="page__lede">
        The normalized events PayMux has published. Each one is delivered to every destination its
        application has registered.
      </p>

      <div className="filters">
        <select aria-label="Event type" value={type} onChange={(event) => setType(event.target.value)}>
          <option value="">Any type</option>
          {TYPES.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
        {type && (
          <button type="button" className="button button--small" onClick={() => setType('')}>
            Clear filter
          </button>
        )}
      </div>

      {events.isError && <ErrorNotice error={events.error} action="Could not load events." />}

      <div className="panel">
        <div className="panel__scroll">
          {events.isPending ? (
            <Loading rows={6} />
          ) : events.data && events.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Event</th>
                  <th>Type</th>
                  <th>Payment</th>
                  <th>Gateway</th>
                  <th>Published</th>
                </tr>
              </thead>
              <tbody>
                {events.data.data.map((event) => (
                  <tr
                    key={event.id}
                    className="is-clickable"
                    onClick={() => setInspecting(event)}
                  >
                    <td>
                      <Id value={event.id} />
                    </td>
                    <td>
                      <Tag tone={tone(event.type)}>{event.type}</Tag>
                    </td>
                    <td>
                      {event.payment_id ? (
                        <Link
                          to={`/payments/${event.payment_id}`}
                          className="mono"
                          onClick={(clickEvent) => clickEvent.stopPropagation()}
                        >
                          {event.payment_id}
                        </Link>
                      ) : (
                        '—'
                      )}
                    </td>
                    <td className="gateway-status">{event.data?.gateway_status || event.gateway}</td>
                    <td>
                      <Timestamp value={event.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : type ? (
            <Empty title={`No ${type} events`} hint="Try another type, or clear the filter." />
          ) : (
            <Empty
              title="No events yet"
              hint="PayMux publishes an event whenever a payment changes state."
            />
          )}
        </div>
      </div>

      {inspecting && (
        <Modal title={inspecting.type} onClose={() => setInspecting(null)}>
          <p className="field__hint" style={{ marginBottom: 10 }}>
            This is the body delivered to the application, signed with its destination secret.
          </p>
          <CodeBlock value={inspecting.data} />
        </Modal>
      )}
    </>
  );
}
