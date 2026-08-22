import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

import { useApplications, usePayments } from '../api/queries';
import type { PaymentStatus } from '../api/types';
import {
  Amount,
  Empty,
  ErrorNotice,
  Id,
  Loading,
  PaymentStatusTag,
  Timestamp,
} from '../components/primitives';

const STATUSES: PaymentStatus[] = [
  'PENDING',
  'AUTHORIZED',
  'PAID',
  'FAILED',
  'CANCELED',
  'EXPIRED',
  'REFUNDED',
  'PARTIALLY_REFUNDED',
];

export function PaymentsPage() {
  const navigate = useNavigate();
  const applications = useApplications({ limit: 100 });

  const [status, setStatus] = useState('');
  const [applicationId, setApplicationId] = useState('');
  const [orderId, setOrderId] = useState('');
  const [cursor, setCursor] = useState<string | undefined>();

  const payments = usePayments({
    status: status || undefined,
    application_id: applicationId || undefined,
    application_order_id: orderId || undefined,
    starting_after: cursor,
    limit: 50,
  });

  // Changing a filter starts a new page sequence; keeping the old cursor
  // would silently skip results.
  const filterChange = (apply: () => void) => {
    apply();
    setCursor(undefined);
  };

  const names = new Map(applications.data?.data.map((app) => [app.id, app.name]));
  const filtered = Boolean(status || applicationId || orderId);

  return (
    <>
      <div className="page__head">
        <h1>Payments</h1>
      </div>
      <p className="page__lede">
        Every payment PayMux has opened, across all applications. Open one to see its full trace.
      </p>

      <div className="filters">
        <select
          aria-label="Application"
          value={applicationId}
          onChange={(event) => filterChange(() => setApplicationId(event.target.value))}
        >
          <option value="">All applications</option>
          {applications.data?.data.map((app) => (
            <option key={app.id} value={app.id}>
              {app.name}
            </option>
          ))}
        </select>

        <select
          aria-label="Status"
          value={status}
          onChange={(event) => filterChange(() => setStatus(event.target.value))}
        >
          <option value="">Any status</option>
          {STATUSES.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>

        <input
          aria-label="Order reference"
          placeholder="Order reference"
          value={orderId}
          onChange={(event) => filterChange(() => setOrderId(event.target.value))}
        />

        {filtered && (
          <button
            type="button"
            className="button button--small"
            onClick={() =>
              filterChange(() => {
                setStatus('');
                setApplicationId('');
                setOrderId('');
              })
            }
          >
            Clear filters
          </button>
        )}
      </div>

      {payments.isError && <ErrorNotice error={payments.error} action="Could not load payments." />}

      <div className="panel">
        <div className="panel__scroll">
          {payments.isPending ? (
            <Loading rows={6} />
          ) : payments.data && payments.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Payment</th>
                  <th>Application</th>
                  <th>Order</th>
                  <th className="num">Amount</th>
                  <th>Status</th>
                  <th>Gateway</th>
                  <th>Method</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {payments.data.data.map((payment) => (
                  <tr
                    key={payment.id}
                    className="is-clickable"
                    onClick={() => navigate(`/payments/${payment.id}`)}
                  >
                    <td>
                      <Link to={`/payments/${payment.id}`} className="mono">
                        {payment.id}
                      </Link>
                    </td>
                    <td>{names.get(payment.application_id) ?? <Id value={payment.application_id} />}</td>
                    <td className="mono">{payment.application_order_id}</td>
                    <td className="num">
                      <Amount minor={payment.amount} currency={payment.currency} />
                    </td>
                    <td>
                      <PaymentStatusTag status={payment.status} />
                    </td>
                    <td className="gateway-status">{payment.gateway_status || '—'}</td>
                    <td className="gateway-status">{payment.payment_type || '—'}</td>
                    <td>
                      <Timestamp value={payment.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : filtered ? (
            <Empty
              title="No payments match these filters"
              hint="Try widening the status or clearing the order reference."
            />
          ) : (
            <Empty
              title="No payments yet"
              hint="Payments appear here as soon as an application creates one through the API."
            />
          )}
        </div>
      </div>

      {payments.data && (payments.data.has_more || cursor) && (
        <div className="actions" style={{ marginTop: 12 }}>
          {cursor && (
            <button type="button" className="button" onClick={() => setCursor(undefined)}>
              Back to newest
            </button>
          )}
          {payments.data.has_more && (
            <button
              type="button"
              className="button"
              onClick={() => setCursor(payments.data.data.at(-1)?.id)}
            >
              Older payments
            </button>
          )}
        </div>
      )}
    </>
  );
}
