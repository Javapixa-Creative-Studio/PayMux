import { Link } from 'react-router-dom';

import { useDeliveries, useGatewayAccounts, useOverview, usePayments } from '../api/queries';
import { FanOut } from '../components/FanOut';
import {
  Amount,
  DeliveryResult,
  DeliveryStateTag,
  Empty,
  ErrorNotice,
  Id,
  Loading,
  PaymentStatusTag,
  Timestamp,
} from '../components/primitives';

/**
 * The overview answers "is anything wrong right now, and what happened
 * lately". The live counters already sit in the strip above, so this page
 * leads with what needs attention rather than repeating them as cards.
 */
export function OverviewPage() {
  const overview = useOverview('24h');
  const recentPayments = usePayments({ limit: 8 });
  const failing = useDeliveries({ state: 'failed', limit: 5 });
  const dead = useDeliveries({ state: 'dead', limit: 5 });
  const gateways = useGatewayAccounts();

  const needsGateway = gateways.data && gateways.data.data.length === 0;
  const attention = [...(failing.data?.data ?? []), ...(dead.data?.data ?? [])];

  return (
    <>
      <div className="page__head">
        <h1>Overview</h1>
      </div>
      <p className="page__lede">
        How payments are flowing from the gateway to each of your applications over the last 24
        hours. Deliveries that are still failing stay listed however long they have been failing.
      </p>

      {needsGateway && (
        <div className="notice notice--warn">
          <div>
            No gateway account is configured yet, so applications cannot create payments.{' '}
            <Link to="/gateways">Add your Midtrans credentials</Link> to get started.
          </div>
        </div>
      )}

      {overview.data && <FanOut overview={overview.data} />}

      {attention.length > 0 && (
        <div className="panel">
          <div className="panel__head">
            <h2>Deliveries needing attention</h2>
            <Link to="/deliveries" className="button button--small">
              All deliveries
            </Link>
          </div>
          <div className="panel__scroll">
            <table>
              <thead>
                <tr>
                  <th>Delivery</th>
                  <th>Destination</th>
                  <th>State</th>
                  <th className="num">Attempts</th>
                  <th>Last result</th>
                  <th>Next attempt</th>
                </tr>
              </thead>
              <tbody>
                {attention.map((delivery) => (
                  <tr key={delivery.id} className="is-failing">
                    <td>
                      <Id value={delivery.id} />
                    </td>
                    <td className="mono" style={{ maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {delivery.url}
                    </td>
                    <td>
                      <DeliveryStateTag state={delivery.state} />
                    </td>
                    <td className="num">
                      {delivery.attempt_count}/{delivery.max_attempts}
                    </td>
                    <td>
                      <DeliveryResult
                        statusCode={delivery.last_status_code}
                        error={delivery.last_error}
                      />
                    </td>
                    <td>
                      {delivery.state === 'dead' ? (
                        <span className="gateway-status">gave up</span>
                      ) : (
                        <Timestamp value={delivery.next_attempt_at} />
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {overview.isError && <ErrorNotice error={overview.error} action="Could not load metrics." />}

      <div className="panel">
        <div className="panel__head">
          <h2>Latest payments</h2>
          <Link to="/payments" className="button button--small">
            All payments
          </Link>
        </div>
        <div className="panel__scroll">
          {recentPayments.isPending ? (
            <Loading rows={5} />
          ) : recentPayments.data && recentPayments.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Payment</th>
                  <th>Order</th>
                  <th className="num">Amount</th>
                  <th>Status</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {recentPayments.data.data.map((payment) => (
                  <tr key={payment.id}>
                    <td>
                      <Link to={`/payments/${payment.id}`} className="mono">
                        {payment.id}
                      </Link>
                    </td>
                    <td className="mono">{payment.application_order_id}</td>
                    <td className="num">
                      <Amount minor={payment.amount} currency={payment.currency} />
                    </td>
                    <td>
                      <PaymentStatusTag status={payment.status} />
                    </td>
                    <td>
                      <Timestamp value={payment.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty
              title="No payments yet"
              hint="Once an application calls POST /api/v1/payments, its payments appear here."
              action={
                <Link to="/applications" className="button button--primary">
                  Set up an application
                </Link>
              }
            />
          )}
        </div>
      </div>
    </>
  );
}
