import { useState, type FormEvent } from 'react';

import {
  useCreateGatewayAccount,
  useDeleteGatewayAccount,
  useGatewayAccounts,
  useGatewayBalance,
  useSupportedGateways,
  useTestGatewayAccount,
  useUpdateGatewayAccount,
} from '../api/queries';
import type { GatewayAccount } from '../api/types';
import { Modal } from '../components/Modal';
import {
  Amount,
  Empty,
  ErrorNotice,
  Loading,
  Tag,
  Timestamp,
} from '../components/primitives';

export function GatewaysPage() {
  const accounts = useGatewayAccounts();
  const supported = useSupportedGateways();
  const update = useUpdateGatewayAccount();
  const remove = useDeleteGatewayAccount();
  const test = useTestGatewayAccount();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<GatewayAccount | null>(null);

  return (
    <>
      <div className="page__head">
        <h1>Gateways</h1>
        <button type="button" className="button button--primary" onClick={() => setAdding(true)}>
          Add account
        </button>
      </div>
      <p className="page__lede">
        The credentials PayMux uses to reach a payment gateway. Server keys are encrypted when saved
        and are never shown again, not here, and not through the API.
      </p>

      {(accounts.isError || update.isError || remove.isError || test.isError) && (
        <ErrorNotice
          error={accounts.error ?? update.error ?? remove.error ?? test.error}
          action={test.isError ? 'The connection test could not run.' : 'That change was not saved.'}
        />
      )}

      <div className="panel">
        <div className="panel__scroll">
          {accounts.isPending ? (
            <Loading rows={3} />
          ) : accounts.data && accounts.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Gateway</th>
                  <th>Environment</th>
                  <th>Merchant</th>
                  <th>Server key</th>
                  <th>State</th>
                  <th>Connection</th>
                  <th className="num">Payout balance</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {accounts.data.data.map((account) => (
                  <tr key={account.id}>
                    <td data-label="Name" data-primary="">
                      {account.name}
                      {account.is_default && (
                        <span className="gateway-status" style={{ marginLeft: 8 }}>
                          default
                        </span>
                      )}
                    </td>
                    <td data-label="Gateway" className="mono">{account.gateway}</td>
                    <td data-label="Environment">
                      <Tag tone={account.environment === 'production' ? 'settled' : 'inert'}>
                        {account.environment}
                      </Tag>
                    </td>
                    <td data-label="Merchant" className="mono">{account.merchant_id || '—'}</td>
                    <td data-label="Server key" className="gateway-status">
                      {account.server_key_set ? '•••••••• set' : 'not set'}
                    </td>
                    <td data-label="State">
                      <Tag tone={account.enabled ? 'settled' : 'inert'}>
                        {account.enabled ? 'enabled' : 'disabled'}
                      </Tag>
                    </td>
                    <td data-label="Connection" className="cell--stack">
                      <ConnectionStatus account={account} />
                    </td>
                    <td data-label="Payout balance" className="num">
                      <PayoutBalance account={account} />
                    </td>
                    <td>
                      <div className="actions">
                        <button
                          type="button"
                          className="button button--small"
                          onClick={() => test.mutate(account.id)}
                          disabled={test.isPending}
                        >
                          {test.isPending && test.variables === account.id
                            ? 'Testing…'
                            : 'Test connection'}
                        </button>
                        <button
                          type="button"
                          className="button button--small"
                          onClick={() => setEditing(account)}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          className="button button--small"
                          onClick={() =>
                            update.mutate({ id: account.id, enabled: !account.enabled })
                          }
                          disabled={update.isPending}
                        >
                          {account.enabled ? 'Disable' : 'Enable'}
                        </button>
                        <button
                          type="button"
                          className="button button--small button--danger"
                          onClick={() => remove.mutate(account.id)}
                          disabled={remove.isPending}
                        >
                          Remove
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty
              title="No gateway account"
              hint="PayMux cannot create payments until it has credentials for a gateway. Start with your Midtrans sandbox keys."
              action={
                <button type="button" className="button button--primary" onClick={() => setAdding(true)}>
                  Add account
                </button>
              }
            />
          )}
        </div>
      </div>

      {accounts.data && accounts.data.data.length > 0 && (
        <div className="panel">
          <div className="panel__head">
            <h2>Notification URL</h2>
          </div>
          <div className="panel__body">
            <p className="field__hint" style={{ marginBottom: 8 }}>
              Set this as the Payment Notification URL in your gateway's own dashboard. PayMux
              verifies every callback against the account's server key before applying it.
            </p>
            <div className="notice__code">{`${window.location.origin.replace(':5173', ':8080')}/webhooks/midtrans`}</div>
          </div>
        </div>
      )}

      {adding && (
        <AccountDialog
          gateways={supported.data?.data.map((entry) => entry.name) ?? ['midtrans']}
          onClose={() => setAdding(false)}
        />
      )}
      {editing && <EditAccountDialog account={editing} onClose={() => setEditing(null)} />}
    </>
  );
}

/**
 * Reports the last connection check.
 *
 * A failed check keeps the gateway's own explanation, because "rejected" and
 * "unreachable" call for different responses from the operator.
 */
function ConnectionStatus({ account }: { account: GatewayAccount }) {
  if (account.last_check_ok === null || account.last_checked_at === null) {
    return <span className="gateway-status">not tested</span>;
  }
  return (
    <div>
      <Tag tone={account.last_check_ok ? 'settled' : 'failed'}>
        {account.last_check_ok ? 'reachable' : 'failed'}
      </Tag>
      <div className="gateway-status" style={{ marginTop: 3 }}>
        <Timestamp value={account.last_checked_at} />
      </div>
      {!account.last_check_ok && account.last_check_error && (
        <div className="gateway-status" style={{ marginTop: 3, maxWidth: 280, whiteSpace: 'normal' }}>
          {account.last_check_error}
        </div>
      )}
    </div>
  );
}

function AccountDialog({ gateways, onClose }: { gateways: string[]; onClose: () => void }) {
  const create = useCreateGatewayAccount();
  const [form, setForm] = useState({
    gateway: gateways[0] ?? 'midtrans',
    name: '',
    environment: 'sandbox',
    merchant_id: '',
    client_key: '',
    server_key: '',
    is_default: true,
  });

  const set = (key: keyof typeof form) => (value: string | boolean) =>
    setForm((current) => ({ ...current, [key]: value }));

  const submit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(form, { onSuccess: onClose });
  };

  return (
    <Modal
      title="Add gateway account"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            form="add-gateway"
            className="button button--primary"
            disabled={!form.name || !form.server_key || create.isPending}
          >
            {create.isPending ? 'Saving…' : 'Save account'}
          </button>
        </>
      }
    >
      <form id="add-gateway" onSubmit={submit}>
        {create.isError && <ErrorNotice error={create.error} action="The account was not saved." />}

        <div className="field">
          <label className="field__label" htmlFor="gw-name">
            Name
          </label>
          <input
            id="gw-name"
            value={form.name}
            onChange={(event) => set('name')(event.target.value)}
            placeholder="Midtrans sandbox"
            required
          />
          <span className="field__hint">How you'll recognise this account in PayMux.</span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="gw-gateway">
            Gateway
          </label>
          <select
            id="gw-gateway"
            value={form.gateway}
            onChange={(event) => set('gateway')(event.target.value)}
          >
            {gateways.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="gw-environment">
            Environment
          </label>
          <select
            id="gw-environment"
            value={form.environment}
            onChange={(event) => set('environment')(event.target.value)}
          >
            <option value="sandbox">Sandbox</option>
            <option value="production">Production</option>
          </select>
          <span className="field__hint">
            Sandbox accounts pair with test API keys, production with live ones.
          </span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="gw-merchant">
            Merchant ID
          </label>
          <input
            id="gw-merchant"
            className="mono"
            value={form.merchant_id}
            onChange={(event) => set('merchant_id')(event.target.value)}
            placeholder="G123456789"
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="gw-client">
            Client key
          </label>
          <input
            id="gw-client"
            className="mono"
            value={form.client_key}
            onChange={(event) => set('client_key')(event.target.value)}
            placeholder="SB-Mid-client-…"
          />
          <span className="field__hint">Safe to expose in a browser; used by the checkout script.</span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="gw-server">
            Server key
          </label>
          <input
            id="gw-server"
            className="mono"
            type="password"
            value={form.server_key}
            onChange={(event) => set('server_key')(event.target.value)}
            placeholder="SB-Mid-server-…"
            required
          />
          <span className="field__hint">
            Encrypted when saved. PayMux will never show it again, so keep your own copy.
          </span>
        </div>

        <div className="field">
          <label className="field__label" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <input
              type="checkbox"
              style={{ width: 'auto' }}
              checked={form.is_default}
              onChange={(event) => set('is_default')(event.target.checked)}
            />
            Use for applications that name no account
          </label>
        </div>
      </form>
    </Modal>
  );
}

function EditAccountDialog({ account, onClose }: { account: GatewayAccount; onClose: () => void }) {
  const update = useUpdateGatewayAccount();
  const [name, setName] = useState(account.name);
  const [merchantId, setMerchantId] = useState(account.merchant_id);
  const [clientKey, setClientKey] = useState(account.client_key);
  const [serverKey, setServerKey] = useState('');
  const [creatorKey, setCreatorKey] = useState('');
  const [approverKey, setApproverKey] = useState('');
  const [isDefault, setIsDefault] = useState(account.is_default);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    update.mutate(
      {
        id: account.id,
        name,
        merchant_id: merchantId,
        client_key: clientKey,
        is_default: isDefault,
        // Only sent when the operator is deliberately replacing it.
        ...(serverKey ? { server_key: serverKey } : {}),
        ...(creatorKey ? { disbursement_creator_key: creatorKey } : {}),
        ...(approverKey ? { disbursement_approver_key: approverKey } : {}),
      },
      { onSuccess: onClose },
    );
  };

  return (
    <Modal
      title={`Edit ${account.name}`}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            form="edit-gateway"
            className="button button--primary"
            disabled={update.isPending}
          >
            {update.isPending ? 'Saving…' : 'Save changes'}
          </button>
        </>
      }
    >
      <form id="edit-gateway" onSubmit={submit}>
        {update.isError && <ErrorNotice error={update.error} action="The changes were not saved." />}

        <div className="field">
          <label className="field__label" htmlFor="edit-name">
            Name
          </label>
          <input id="edit-name" value={name} onChange={(event) => setName(event.target.value)} />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="edit-merchant">
            Merchant ID
          </label>
          <input
            id="edit-merchant"
            className="mono"
            value={merchantId}
            onChange={(event) => setMerchantId(event.target.value)}
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="edit-client">
            Client key
          </label>
          <input
            id="edit-client"
            className="mono"
            value={clientKey}
            onChange={(event) => setClientKey(event.target.value)}
          />
        </div>

        {/*
          * Disbursement is a separate Midtrans product with separate approval,
          * so these are separate fields rather than more of the same. Leaving
          * them blank changes nothing; an account without a creator key simply
          * cannot pay out, which is the safe default.
          */}
        <div className="notice" style={{ margin: '18px 0 12px' }}>
          <strong>Paying money out</strong>
          <div className="field__hint" style={{ marginTop: 4 }}>
            Midtrans issues these separately from the payment keys, and only once your
            account is approved for disbursement. Whoever holds the creator key can request a
            payout; only the approver key can release one.
          </div>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="edit-creator">
            Disbursement creator key
          </label>
          <input
            id="edit-creator"
            className="mono"
            type="password"
            value={creatorKey}
            onChange={(event) => setCreatorKey(event.target.value)}
            placeholder="Leave blank to keep the current one"
          />
          <span className="field__hint">
            Without this, this account cannot pay out at all.
          </span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="edit-approver">
            Disbursement approver key
          </label>
          <input
            id="edit-approver"
            className="mono"
            type="password"
            value={approverKey}
            onChange={(event) => setApproverKey(event.target.value)}
            placeholder="Leave blank to keep the current one"
          />
          <span className="field__hint">
            Optional. Without it, payouts must be released in the Midtrans dashboard instead.
          </span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="edit-server">
            Replace server key
          </label>
          <input
            id="edit-server"
            className="mono"
            type="password"
            value={serverKey}
            onChange={(event) => setServerKey(event.target.value)}
            placeholder="Leave empty to keep the current key"
          />
        </div>

        <div className="field">
          <label className="field__label" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <input
              type="checkbox"
              style={{ width: 'auto' }}
              checked={isDefault}
              onChange={(event) => setIsDefault(event.target.checked)}
            />
            Use for applications that name no account
          </label>
        </div>
      </form>
    </Modal>
  );
}


/**
 * The disbursement balance, asked for only when this account can disburse.
 *
 * PayMux never gates a payout on this number: the per-application limits do
 * that, and a balance is a snapshot that can be stale by the time a transfer
 * runs. It is here so an operator can see whether there is money to send.
 */
function PayoutBalance({ account }: { account: GatewayAccount }) {
  // Read from the keys, not from capabilities: that blob only refreshes on a
  // connection test, so it would claim an account cannot pay out until
  // somebody happened to press a button.
  const canDisburse = account.disbursement_creator_key_set;
  const balance = useGatewayBalance(account.id, canDisburse);

  if (!canDisburse) {
    return <span className="gateway-status">no payout keys</span>;
  }
  if (balance.isPending) {
    return <span className="gateway-status">…</span>;
  }
  if (balance.isError || !balance.data) {
    // The gateway refused or does not answer. Saying so is more use than a
    // blank cell, because a balance that will not load is itself a finding.
    return <span className="gateway-status">unavailable</span>;
  }
  return <Amount minor={balance.data.amount} currency={balance.data.currency} />;
}
