import { useState } from 'react';
import { Link } from 'react-router-dom';

import { useApplications, useRefunds } from '../api/queries';
import type { Refund } from '../api/types';
import {
  Amount,
  Empty,
  ErrorNotice,
  Id,
  Loading,
  Tag,
  Timestamp,
} from '../components/primitives';

const STATUSES: Array<Refund['status']> = ['PENDING', 'SUCCEEDED', 'FAILED'];

function tone(status: Refund['status']): 'settled' | 'pending' | 'failed' {
  switch (status) {
    case 'SUCCEEDED':
      return 'settled';
    case 'FAILED':
      return 'failed';
    default:
      return 'pending';
  }
}

/**
 * Refunds across every payment.
 *
 * A payment's own refunds live on its detail page; this view answers the other
 * question — what have we refunded lately, and did any of it fail — which is
 * what an operator reconciling a day's activity arrives with.
 */
export function RefundsPage() {
  const applications = useApplications({ limit: 100 });
  const [status, setStatus] = useState('');
  const [applicationId, setApplicationId] = useState('');

  const refunds = useRefunds({
    status: status || undefined,
    application_id: applicationId || undefined,
    limit: 50,
  });

  const names = new Map(applications.data?.data.map((app) => [app.id, app.name]));
  const filtered = Boolean(status || applicationId);

  return (
    <>
      <div className="page__head">
        <h1>Refunds</h1>
      </div>
      <p className="page__lede">
        Every refund PayMux has sent to a gateway. A failed refund stays here with the reason the
        gateway gave.
      </p>

      <div className="filters">
        <select
          aria-label="Application"
          value={applicationId}
          onChange={(event) => setApplicationId(event.target.value)}
        >
          <option value="">All applications</option>
          {applications.data?.data.map((app) => (
            <option key={app.id} value={app.id}>
              {app.name}
            </option>
          ))}
        </select>

        <select aria-label="Status" value={status} onChange={(event) => setStatus(event.target.value)}>
          <option value="">Any status</option>
          {STATUSES.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>

        {filtered && (
          <button
            type="button"
            className="button button--small"
            onClick={() => {
              setStatus('');
              setApplicationId('');
            }}
          >
            Clear filters
          </button>
        )}
      </div>

      {refunds.isError && <ErrorNotice error={refunds.error} action="Could not load refunds." />}

      <div className="panel">
        <div className="panel__scroll">
          {refunds.isPending ? (
            <Loading rows={5} />
          ) : refunds.data && refunds.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Refund</th>
                  <th>Payment</th>
                  <th>Application</th>
                  <th className="num">Amount</th>
                  <th>Status</th>
                  <th>Reason</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {refunds.data.data.map((refund) => (
                  <tr key={refund.id}>
                    <td data-label="Refund" data-primary="">
                      <Id value={refund.id} />
                    </td>
                    <td data-label="Payment">
                      <Link to={`/payments/${refund.payment_id}`} className="mono">
                        {refund.payment_id}
                      </Link>
                    </td>
                    <td data-label="Application">
                      {names.get(refund.application_id ?? '') ?? '—'}
                    </td>
                    <td data-label="Amount" className="num">
                      <Amount minor={refund.amount} currency={refund.currency} />
                    </td>
                    <td data-label="Status">
                      <Tag tone={tone(refund.status)}>{refund.status}</Tag>
                    </td>
                    <td data-label="Reason" className="cell--stack">
                      {refund.failure_reason || refund.reason || '—'}
                    </td>
                    <td data-label="Created">
                      <Timestamp value={refund.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : filtered ? (
            <Empty title="No refunds match these filters" hint="Try another status or application." />
          ) : (
            <Empty
              title="No refunds yet"
              hint="Refund a settled payment from its detail page, or through the API."
            />
          )}
        </div>
      </div>
    </>
  );
}
