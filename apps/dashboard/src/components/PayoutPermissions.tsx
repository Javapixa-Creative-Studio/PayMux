import { useEffect, useState, type FormEvent } from 'react';

import {
  useBeneficiaries,
  usePayoutLimits,
  useSetPayoutLimits,
  useVerifyBeneficiary,
} from '../api/queries';
import { Empty, ErrorNotice, Loading, Tag, Timestamp } from './primitives';

/**
 * Whether the bank's answer looks like the name on file.
 *
 * Deliberately forgiving: banks abbreviate, drop honorifics and uppercase
 * everything, so an exact comparison would flag almost every real account.
 * This only asks whether one contains the other once both are reduced to
 * letters, and a mismatch is a prompt to look rather than a verdict.
 */
function nameMatches(onFile: string, atBank: string | undefined): boolean {
  if (!atBank) return true;
  const reduce = (v: string) => v.toLowerCase().replace(/[^a-z]/g, '');
  const a = reduce(onFile);
  const b = reduce(atBank);
  return a.includes(b) || b.includes(a);
}

/**
 * What an application may pay out, and where to.
 *
 * This panel is the switch for the whole feature: until somebody turns it on
 * here, an application holding a valid API key still cannot move a rupiah. It
 * says so plainly rather than showing an unexplained toggle, because the
 * default being "off" is a decision worth understanding, not a setting that
 * looks like an oversight.
 */
export function PayoutPermissions({ applicationId }: { applicationId: string }) {
  const limits = usePayoutLimits(applicationId);
  const save = useSetPayoutLimits(applicationId);
  const beneficiaries = useBeneficiaries(applicationId);
  const verify = useVerifyBeneficiary(applicationId);

  const [enabled, setEnabled] = useState(false);
  const [requiresApproval, setRequiresApproval] = useState(true);
  const [maxAmount, setMaxAmount] = useState('');
  const [dailyLimit, setDailyLimit] = useState('');

  // Seed the form from the server once it answers, so an operator edits what
  // is actually configured rather than a guess.
  useEffect(() => {
    if (!limits.data) return;
    setEnabled(limits.data.enabled);
    setRequiresApproval(limits.data.requires_approval);
    setMaxAmount(limits.data.max_amount == null ? '' : String(limits.data.max_amount));
    setDailyLimit(limits.data.daily_limit == null ? '' : String(limits.data.daily_limit));
  }, [limits.data]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const max = maxAmount.trim();
    const daily = dailyLimit.trim();
    save.mutate({
      enabled,
      requires_approval: requiresApproval,
      // An empty field means "no ceiling", which the API insists is said in
      // words rather than inferred from a missing value.
      ...(max ? { max_amount: Number(max) } : { clear_max_amount: true }),
      ...(daily ? { daily_limit: Number(daily) } : { clear_daily_limit: true }),
    });
  };

  const rows = beneficiaries.data?.data ?? [];

  return (
    <div className="panel">
      <div className="panel__head">
        <h2>Paying money out</h2>
        {limits.data && (
          <Tag tone={limits.data.enabled ? 'settled' : 'inert'}>
            {limits.data.enabled ? 'enabled' : 'off'}
          </Tag>
        )}
      </div>

      <div className="panel__body">
        {(limits.isError || save.isError || verify.isError) && (
          <ErrorNotice
            error={limits.error ?? save.error ?? verify.error}
            action={
              verify.isError
                ? 'The bank could not confirm that account.'
                : save.isError
                  ? 'The limits were not saved.'
                  : 'Could not load the limits.'
            }
          />
        )}

        {limits.isPending ? (
          <Loading rows={3} />
        ) : (
          <form onSubmit={submit}>
            <div className="field">
              <label className="field__label" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <input
                  type="checkbox"
                  style={{ width: 'auto' }}
                  checked={enabled}
                  onChange={(event) => setEnabled(event.target.checked)}
                />
                Let this application pay money out
              </label>
              <span className="field__hint">
                Off by default. Every application shares one merchant balance, so an application
                that can disburse can spend money the others took.
              </span>
            </div>

            <div className="field">
              <label className="field__label" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <input
                  type="checkbox"
                  style={{ width: 'auto' }}
                  checked={requiresApproval}
                  onChange={(event) => setRequiresApproval(event.target.checked)}
                  disabled={!enabled}
                />
                Require someone to approve each payout
              </label>
              <span className="field__hint">
                With this off, an API key alone releases the money. PayMux will insist on a limit
                instead.
              </span>
            </div>

            <div className="field">
              <label className="field__label" htmlFor="payout-max">
                Most per payout
              </label>
              <input
                id="payout-max"
                className="mono"
                inputMode="numeric"
                value={maxAmount}
                onChange={(event) => setMaxAmount(event.target.value.replace(/[^0-9]/g, ''))}
                placeholder="No ceiling"
                disabled={!enabled}
              />
              <span className="field__hint">In minor units: 500000 is Rp 500.000.</span>
            </div>

            <div className="field">
              <label className="field__label" htmlFor="payout-daily">
                Most per day
              </label>
              <input
                id="payout-daily"
                className="mono"
                inputMode="numeric"
                value={dailyLimit}
                onChange={(event) => setDailyLimit(event.target.value.replace(/[^0-9]/g, ''))}
                placeholder="No ceiling"
                disabled={!enabled}
              />
              <span className="field__hint">
                Counts everything committed in the last 24 hours, including payouts still in
                flight: money the gateway already has cannot be spent twice.
              </span>
            </div>

            <button type="submit" className="button button--primary" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : 'Save payout settings'}
            </button>
          </form>
        )}
      </div>

      <div className="panel__head" style={{ borderTop: '1px solid var(--rule)' }}>
        <h2>Beneficiaries</h2>
        <span className="fanout__caption">
          {rows.length} destination{rows.length === 1 ? '' : 's'}
        </span>
      </div>

      <div className="panel__scroll">
        {beneficiaries.isPending ? (
          <Loading rows={3} />
        ) : rows.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Alias</th>
                <th>Name</th>
                <th>Account</th>
                <th>Account holder</th>
                <th>Added</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((b) => (
                <tr key={b.id} className={b.disabled_at ? 'is-attention' : undefined}>
                  <td data-label="Alias" data-primary="" className="mono">
                    {b.alias}
                  </td>
                  <td data-label="Name">{b.name}</td>
                  <td data-label="Account" className="mono">
                    {b.bank.toUpperCase()} {b.account}
                  </td>
                  <td data-label="Account holder" className="cell--stack">
                    {b.disabled_at ? (
                      <Tag tone="inert">disabled</Tag>
                    ) : b.verified_at ? (
                      // The bank's own answer, shown verbatim. A name that
                      // differs is not necessarily wrong: that judgement is
                      // the operator's, and they can only make it if they can
                      // see what the bank actually said.
                      <span className={nameMatches(b.name, b.verified_name) ? '' : 'gateway-status'}>
                        {b.verified_name || '—'}
                        {!nameMatches(b.name, b.verified_name) && (
                          <span style={{ color: 'var(--signal-pending)' }}> · differs</span>
                        )}
                      </span>
                    ) : (
                      <span className="gateway-status">not checked</span>
                    )}
                  </td>
                  <td data-label="Added">
                    <Timestamp value={b.created_at} />
                  </td>
                  <td>
                    <button
                      type="button"
                      className="button button--small"
                      onClick={() => verify.mutate(b.id)}
                      disabled={verify.isPending}
                    >
                      {verify.isPending && verify.variables === b.id ? 'Checking…' : 'Check account'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty
            title="No beneficiaries"
            hint="The application adds them by calling POST /api/v1/beneficiaries. A payout has to name one, so a typo cannot send money to a stranger."
          />
        )}
      </div>
    </div>
  );
}
