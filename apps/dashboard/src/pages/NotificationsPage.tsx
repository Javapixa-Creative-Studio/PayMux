import { useState } from 'react';
import { Link } from 'react-router-dom';

import { useGatewayEvents } from '../api/queries';
import type { GatewayEvent, RoutingStatus } from '../api/types';
import { Modal } from '../components/Modal';
import {
  CodeBlock,
  Empty,
  ErrorNotice,
  Id,
  KeyValue,
  Loading,
  RoutingTag,
  Tag,
  Timestamp,
} from '../components/primitives';

const ROUTINGS: RoutingStatus[] = ['routed', 'duplicate', 'unrouted', 'rejected', 'ignored'];

/** What each routing outcome means, in the operator's terms. */
const EXPLANATION: Record<RoutingStatus, string> = {
  routed: 'Attributed to a payment and applied.',
  duplicate: 'A repeat of a state PayMux had already handled. Nothing was published again.',
  unrouted: 'Authentic, but no payment matches this order. Kept for you to investigate.',
  rejected: 'The signature did not verify. Nothing was applied.',
  ignored: 'Understood but changed nothing — a stale state, or a status PayMux does not map.',
};

export function NotificationsPage() {
  const [routing, setRouting] = useState('');
  const [inspecting, setInspecting] = useState<GatewayEvent | null>(null);
  const notifications = useGatewayEvents({ routing_status: routing || undefined, limit: 50 });

  return (
    <>
      <div className="page__head">
        <h1>Gateway notifications</h1>
      </div>
      <p className="page__lede">
        Every callback the gateway has sent, including the ones PayMux could not attribute or could
        not verify. Nothing is discarded.
      </p>

      <div className="filters">
        <select
          aria-label="Routing outcome"
          value={routing}
          onChange={(event) => setRouting(event.target.value)}
        >
          <option value="">Any outcome</option>
          {ROUTINGS.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
        {routing && (
          <>
            <button type="button" className="button button--small" onClick={() => setRouting('')}>
              Clear filter
            </button>
            <span className="field__hint">{EXPLANATION[routing as RoutingStatus]}</span>
          </>
        )}
      </div>

      {notifications.isError && (
        <ErrorNotice error={notifications.error} action="Could not load notifications." />
      )}

      <div className="panel">
        <div className="panel__scroll">
          {notifications.isPending ? (
            <Loading rows={6} />
          ) : notifications.data && notifications.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Received</th>
                  <th>Gateway order</th>
                  <th>Gateway status</th>
                  <th>Signature</th>
                  <th>Outcome</th>
                  <th>Payment</th>
                </tr>
              </thead>
              <tbody>
                {notifications.data.data.map((notification) => (
                  <tr
                    key={notification.id}
                    className={
                      notification.routing_status === 'rejected' ||
                      notification.routing_status === 'unrouted'
                        ? 'is-clickable is-failing'
                        : 'is-clickable'
                    }
                    onClick={() => setInspecting(notification)}
                  >
                    <td data-label="Received">
                      <Timestamp value={notification.received_at} />
                    </td>
                    <td data-label="Gateway order" data-primary="" className="mono">
                      {notification.gateway_order_id || '—'}
                    </td>
                    <td data-label="Gateway status" className="gateway-status">
                      {notification.gateway_status || '—'}
                      {notification.fraud_status ? ` · ${notification.fraud_status}` : ''}
                    </td>
                    <td data-label="Signature">
                      <Tag tone={notification.signature_verified ? 'settled' : 'failed'}>
                        {notification.signature_verified ? 'verified' : 'invalid'}
                      </Tag>
                    </td>
                    <td data-label="Outcome">
                      <RoutingTag status={notification.routing_status} />
                    </td>
                    <td data-label="Payment">
                      {notification.payment_id ? (
                        <Link
                          to={`/payments/${notification.payment_id}`}
                          className="mono"
                          onClick={(event) => event.stopPropagation()}
                        >
                          {notification.payment_id}
                        </Link>
                      ) : (
                        '—'
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : routing ? (
            <Empty
              title={`No ${routing} notifications`}
              hint={EXPLANATION[routing as RoutingStatus]}
            />
          ) : (
            <Empty
              title="No notifications yet"
              hint="Set your gateway's notification URL to /webhooks/midtrans on this host, then take a test payment."
            />
          )}
        </div>
      </div>

      {inspecting && (
        <Modal title="Gateway notification" onClose={() => setInspecting(null)}>
          <dl className="kv" style={{ marginBottom: 14 }}>
            <KeyValue label="Received">
              <Timestamp value={inspecting.received_at} absolute />
            </KeyValue>
            <KeyValue label="Outcome">
              <RoutingTag status={inspecting.routing_status} />
            </KeyValue>
            <KeyValue label="What that means">
              {EXPLANATION[inspecting.routing_status]}
            </KeyValue>
            {inspecting.routing_error && (
              <KeyValue label="Detail">{inspecting.routing_error}</KeyValue>
            )}
            <KeyValue label="Gateway order">
              <span className="mono">{inspecting.gateway_order_id || '—'}</span>
            </KeyValue>
            <KeyValue label="Transaction">
              <Id value={inspecting.gateway_transaction_id} />
            </KeyValue>
          </dl>
          <CodeBlock value={inspecting.payload} />
        </Modal>
      )}
    </>
  );
}
