import { Link, useParams } from 'react-router-dom';

import { usePayoutDetail, useSyncPayout } from '../api/queries';
import type { PayoutTransition } from '../api/types';
import {
  Amount,
  ErrorNotice,
  Id,
  KeyValue,
  Loading,
  PayoutStatusTag,
  Timestamp,
} from '../components/primitives';
import { PAYOUT_MEANING } from '../lib/payouts';

/**
 * One payout, and who did what to it.
 *
 * A payment's detail page leads with the trace because the question is what
 * the gateway said. A payout's leads with the same thing for a different
 * reason: the record of who released it exists nowhere else, and it is the
 * question somebody will eventually ask.
 */
export function PayoutDetailPage() {
  const { payoutId = '' } = useParams();
  const detail = usePayoutDetail(payoutId);
  const sync = useSyncPayout(payoutId);

  if (detail.isPending) return <Loading rows={8} />;
  if (detail.isError || !detail.data) {
    return <ErrorNotice error={detail.error} action="Could not load this payout." />;
  }

  const { payout, transitions } = detail.data;
  // The three terminal states, matching the domain's transition table. Asking
  // the gateway about one cannot change it, so the button says so by being off
  // rather than by failing when pressed.
  const finished =
    payout.status === 'COMPLETED' || payout.status === 'FAILED' || payout.status === 'REJECTED';

  return (
    <>
      <div className="page__head">
        <div>
          <div className="page__eyebrow">{payout.application_payout_id}</div>
          <h1 className="page__amount">
            <Amount minor={payout.amount} currency={payout.currency} />
          </h1>
        </div>
        <Link to="/payouts" className="button button--small">
          All payouts
        </Link>
      </div>

      {sync.isError && (
        <ErrorNotice error={sync.error} action="The gateway could not be reached." />
      )}

      <div className="summary" style={{ marginTop: 14 }}>
        <div className="summary__meta">
          <PayoutStatusTag status={payout.status} />
          <span className="gateway-status">{PAYOUT_MEANING[payout.status]}</span>
        </div>

        <div className="actions">
          <button
            type="button"
            className="button"
            onClick={() => sync.mutate()}
            disabled={sync.isPending || finished}
            title={finished ? 'This payout has finished; there is nothing left to ask about.' : ''}
          >
            {sync.isPending ? 'Asking…' : 'Ask the gateway'}
          </button>
        </div>
      </div>

      {payout.status === 'UNRESOLVED' && (
        <div className="notice notice--warn">
          <div>
            <strong>PayMux does not know whether this money left.</strong> It is re-asking the
            gateway under the original idempotency key, which returns the original result rather
            than sending again. <strong>Do not create a replacement payout</strong> — a new payout
            carries a new key, and the gateway would treat it as a second instruction.
          </div>
        </div>
      )}

      <div className="detail">
        <div className="panel">
          <div className="panel__head">
            <h2>History</h2>
            <span className="fanout__caption">who did what, and when</span>
          </div>
          <div className="panel__body">
            {transitions && transitions.length > 0 ? (
              <ol className="trace">
                {transitions.map((t) => (
                  <li className="trace__entry" key={t.id}>
                    <div className="trace__time">
                      <Timestamp value={t.created_at} />
                    </div>
                    <div className="trace__spine">
                      <div className={`trace__node ${nodeTone(t)}`} />
                    </div>
                    <div className="trace__body">
                      <div className="trace__headline">
                        {describe(t)}
                        <span className="trace__actor">{t.actor_kind}</span>
                      </div>
                      {t.actor_id && (
                        <div className="trace__detail">
                          <Id value={t.actor_id} />
                        </div>
                      )}
                      {t.reason && <div className="trace__meta">{t.reason}</div>}
                    </div>
                  </li>
                ))}
              </ol>
            ) : (
              <p className="empty__hint">Nothing has happened to this payout yet.</p>
            )}
          </div>
        </div>

        <div>
          <div className="panel">
            <div className="panel__head">
              <h2>Destination</h2>
            </div>
            <div className="panel__body">
              <dl className="kv">
                <KeyValue label="Name">{payout.beneficiary_name}</KeyValue>
                <KeyValue label="Account">
                  <span className="mono">
                    {payout.beneficiary_bank.toUpperCase()} {payout.beneficiary_account}
                  </span>
                </KeyValue>
                {payout.notes && <KeyValue label="Note">{payout.notes}</KeyValue>}
              </dl>
            </div>
          </div>

          <div className="panel">
            <div className="panel__head">
              <h2>Details</h2>
            </div>
            <div className="panel__body">
              <dl className="kv">
                <KeyValue label="Application">
                  <Link to={`/applications/${payout.application_id}`}>
                    <Id value={payout.application_id} />
                  </Link>
                </KeyValue>
                <KeyValue label="Gateway reference">
                  {payout.reference_no ? (
                    <span className="mono">{payout.reference_no}</span>
                  ) : (
                    <span className="gateway-status">none yet</span>
                  )}
                </KeyValue>
                {payout.gateway_status && (
                  <KeyValue label="Gateway said">{payout.gateway_status}</KeyValue>
                )}
                {payout.failure_reason && (
                  <KeyValue label="Reason">{payout.failure_reason}</KeyValue>
                )}
                {payout.reject_reason && (
                  <KeyValue label="Refused because">{payout.reject_reason}</KeyValue>
                )}
                <KeyValue label="Requested">
                  <Timestamp value={payout.created_at} absolute />
                </KeyValue>
                {payout.completed_at && (
                  <KeyValue label="Completed">
                    <Timestamp value={payout.completed_at} absolute />
                  </KeyValue>
                )}
              </dl>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}

/** Reads the transition as a sentence about what somebody did. */
function describe(t: PayoutTransition): string {
  switch (t.to_status) {
    case 'REQUESTED':
      return 'Requested';
    case 'APPROVED':
      return 'Approved and released';
    case 'REJECTED':
      return 'Refused';
    case 'SUBMITTED':
      return 'Sent to the gateway';
    case 'UNRESOLVED':
      return 'Sent, outcome unknown';
    case 'COMPLETED':
      return 'The beneficiary was paid';
    case 'FAILED':
      return 'Did not go through';
    default:
      return t.to_status;
  }
}

function nodeTone(t: PayoutTransition): string {
  switch (t.to_status) {
    case 'COMPLETED':
      return 'trace__node--settled';
    case 'FAILED':
    case 'UNRESOLVED':
      return 'trace__node--failed';
    case 'REQUESTED':
    case 'APPROVED':
    case 'SUBMITTED':
      return 'trace__node--pending';
    default:
      return '';
  }
}
