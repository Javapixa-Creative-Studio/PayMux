import { useState } from 'react';

import { useApplications, usePayoutDecision, usePayouts } from '../api/queries';
import type { Payout, PayoutStatus } from '../api/types';
import { Modal } from '../components/Modal';
import {
  Amount,
  Empty,
  ErrorNotice,
  Id,
  Loading,
  PayoutStatusTag,
  Timestamp,
} from '../components/primitives';
import { PAYOUT_MEANING } from '../lib/payouts';

const STATUSES: PayoutStatus[] = [
  'REQUESTED',
  'APPROVED',
  'SUBMITTED',
  'UNRESOLVED',
  'COMPLETED',
  'FAILED',
  'REJECTED',
];

/**
 * Payouts, led by the ones waiting on a decision.
 *
 * Every other list in this console is ordered by time, because the question is
 * what happened. This one is ordered by what needs a person: a payout nobody
 * releases is simply never paid, and it will not announce itself.
 */
export function PayoutsPage() {
  const [status, setStatus] = useState('');
  const [deciding, setDeciding] = useState<Payout | null>(null);

  const payouts = usePayouts({ status: status || undefined, limit: 50 });
  const pending = usePayouts({ status: 'REQUESTED', limit: 50 });
  const applications = useApplications();

  const names = new Map((applications.data?.data ?? []).map((app) => [app.id, app.name]));
  const awaiting = pending.data?.data ?? [];

  return (
    <>
      <div className="page__head">
        <h1>Payouts</h1>
      </div>
      <p className="page__lede">
        Money leaving the merchant balance. A payout waiting for approval has moved nothing yet and
        stays here until somebody decides.
      </p>

      {(payouts.isError || pending.isError) && (
        <ErrorNotice error={payouts.error ?? pending.error} action="Could not load payouts." />
      )}

      {awaiting.length > 0 && !status && (
        <div className="panel">
          <div className="panel__head">
            <h2>Waiting for approval</h2>
            <span className="fanout__caption">
              {total(awaiting)} across {awaiting.length} payout{awaiting.length === 1 ? '' : 's'}
            </span>
          </div>
          <div className="panel__scroll">
            <table>
              <thead>
                <tr>
                  <th>Reference</th>
                  <th>Application</th>
                  <th>To</th>
                  <th className="num">Amount</th>
                  <th>Requested</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {awaiting.map((payout) => (
                  <tr key={payout.id} className="is-attention">
                    <td data-label="Reference" data-primary="" className="mono">
                      {payout.application_payout_id}
                    </td>
                    <td data-label="Application">
                      {names.get(payout.application_id) ?? <Id value={payout.application_id} />}
                    </td>
                    <td data-label="To">
                      {payout.beneficiary_name}{' '}
                      <span className="gateway-status">
                        {payout.beneficiary_bank} {payout.beneficiary_account}
                      </span>
                    </td>
                    <td data-label="Amount" className="num">
                      <Amount minor={payout.amount} currency={payout.currency} />
                    </td>
                    <td data-label="Requested">
                      <Timestamp value={payout.created_at} />
                    </td>
                    <td>
                      <button
                        type="button"
                        className="button button--primary button--small"
                        onClick={() => setDeciding(payout)}
                      >
                        Review
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      <div className="filters" style={{ marginTop: 16 }}>
        <select
          aria-label="Status"
          value={status}
          onChange={(event) => setStatus(event.target.value)}
        >
          <option value="">Any status</option>
          {STATUSES.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
        {status && (
          <>
            <button type="button" className="button button--small" onClick={() => setStatus('')}>
              Clear filter
            </button>
            <span className="field__hint">{PAYOUT_MEANING[status as PayoutStatus]}</span>
          </>
        )}
      </div>

      <div className="panel">
        <div className="panel__scroll">
          {payouts.isPending ? (
            <Loading rows={6} />
          ) : payouts.data && payouts.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Reference</th>
                  <th>Application</th>
                  <th>To</th>
                  <th className="num">Amount</th>
                  <th>Status</th>
                  <th>Result</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {payouts.data.data.map((payout) => (
                  <tr
                    key={payout.id}
                    className={rowClass(payout.status)}
                    onClick={() => setDeciding(payout)}
                    style={{ cursor: 'pointer' }}
                  >
                    <td data-label="Reference" data-primary="" className="mono">
                      {payout.application_payout_id}
                    </td>
                    <td data-label="Application">
                      {names.get(payout.application_id) ?? <Id value={payout.application_id} />}
                    </td>
                    <td data-label="To">{payout.beneficiary_name}</td>
                    <td data-label="Amount" className="num">
                      <Amount minor={payout.amount} currency={payout.currency} />
                    </td>
                    <td data-label="Status">
                      <PayoutStatusTag status={payout.status} />
                    </td>
                    <td data-label="Result" className="gateway-status cell--stack">
                      {payout.failure_reason || payout.reject_reason || payout.gateway_status || '—'}
                    </td>
                    <td data-label="Created">
                      <Timestamp value={payout.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : status ? (
            <Empty title={`No ${status} payouts`} hint={PAYOUT_MEANING[status as PayoutStatus]} />
          ) : (
            <Empty
              title="No payouts yet"
              hint="An application creates one by calling POST /api/v1/payouts, once you have turned payouts on for it."
            />
          )}
        </div>
      </div>

      {deciding && <PayoutDialog payout={deciding} onClose={() => setDeciding(null)} />}
    </>
  );
}

// The heading names the decision being made, not the one that was offered
// first. Asking "Approve this payout?" above a refusal form is a small lie.
function modalTitle(pending: boolean, rejecting: boolean): string {
  if (!pending) return 'Payout';
  return rejecting ? 'Refuse this payout?' : 'Approve this payout?';
}

function rowClass(status: PayoutStatus): string | undefined {
  if (status === 'UNRESOLVED' || status === 'FAILED') return 'is-failing';
  if (status === 'REQUESTED') return 'is-attention';
  return undefined;
}

function total(payouts: Payout[]): string {
  const sum = payouts.reduce((n, p) => n + p.amount, 0);
  const currency = payouts[0]?.currency ?? 'IDR';
  return `${currency} ${sum.toLocaleString('en-US')}`;
}

/**
 * The decision itself.
 *
 * The destination is shown at full size rather than as a table cell, because
 * approving is the moment somebody becomes answerable for where this money
 * goes, and the account number is the part they are answerable for.
 */
function PayoutDialog({ payout, onClose }: { payout: Payout; onClose: () => void }) {
  const [reason, setReason] = useState('');
  const [rejecting, setRejecting] = useState(false);
  const decide = usePayoutDecision(payout.id);

  const pending = payout.status === 'REQUESTED';

  return (
    <Modal title={modalTitle(pending, rejecting)} onClose={onClose}>
      {decide.isError && (
        <ErrorNotice error={decide.error} action="The decision was not recorded." />
      )}

      <div className="summary" style={{ marginBottom: 14 }}>
        <div>
          <div className="summary__amount">
            <Amount minor={payout.amount} currency={payout.currency} />
          </div>
          <div className="summary__meta">
            <PayoutStatusTag status={payout.status} />
            <span className="gateway-status">{payout.application_payout_id}</span>
          </div>
        </div>
      </div>

      <dl className="kv" style={{ marginBottom: 14 }}>
        <dt>To</dt>
        <dd>
          <strong>{payout.beneficiary_name}</strong>
        </dd>
        <dt>Account</dt>
        <dd className="mono">
          {payout.beneficiary_bank.toUpperCase()} {payout.beneficiary_account}
        </dd>
        {payout.notes && (
          <>
            <dt>Note</dt>
            <dd>{payout.notes}</dd>
          </>
        )}
        <dt>What this means</dt>
        <dd>{PAYOUT_MEANING[payout.status]}</dd>
        {payout.reference_no && (
          <>
            <dt>Gateway reference</dt>
            <dd className="mono">{payout.reference_no}</dd>
          </>
        )}
        {payout.failure_reason && (
          <>
            <dt>Reason</dt>
            <dd>{payout.failure_reason}</dd>
          </>
        )}
        {payout.reject_reason && (
          <>
            <dt>Refused because</dt>
            <dd>{payout.reject_reason}</dd>
          </>
        )}
      </dl>

      {pending && rejecting && (
        <div className="field">
          <label className="field__label" htmlFor="reject-reason">
            Why are you refusing this?
          </label>
          <input
            id="reject-reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder="Recorded against the payout"
          />
        </div>
      )}

      {pending ? (
        <div className="actions">
          {rejecting ? (
            <>
              <button
                type="button"
                className="button button--danger"
                disabled={decide.isPending}
                onClick={() =>
                  decide.mutate({ action: 'reject', reason }, { onSuccess: onClose })
                }
              >
                {decide.isPending ? 'Refusing…' : 'Refuse this payout'}
              </button>
              <button type="button" className="button" onClick={() => setRejecting(false)}>
                Back
              </button>
            </>
          ) : (
            <>
              <button
                type="button"
                className="button button--primary"
                disabled={decide.isPending}
                onClick={() => decide.mutate({ action: 'approve' }, { onSuccess: onClose })}
              >
                {decide.isPending ? 'Approving…' : 'Approve and send'}
              </button>
              <button type="button" className="button" onClick={() => setRejecting(true)}>
                Refuse
              </button>
            </>
          )}
        </div>
      ) : (
        <p className="field__hint">
          This payout is no longer awaiting a decision, so it cannot be approved or refused.
        </p>
      )}
    </Modal>
  );
}
