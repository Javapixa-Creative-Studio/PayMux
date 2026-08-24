import { useState, type FormEvent } from 'react';
import { Link, useParams } from 'react-router-dom';

import {
  useApiKeys,
  useApplication,
  useCreateApiKey,
  useCreateDestination,
  useDeleteDestination,
  useDestinations,
  useRevokeApiKey,
  useRotateDestinationSecret,
  useUpdateApplication,
  useUpdateDestination,
} from '../api/queries';
import { Modal } from '../components/Modal';
import { Empty, ErrorNotice, Id, KeyValue, Loading, Tag, Timestamp } from '../components/primitives';

export function ApplicationDetailPage() {
  const { applicationId = '' } = useParams();
  const application = useApplication(applicationId);
  const update = useUpdateApplication(applicationId);

  if (application.isPending) return <Loading rows={6} />;
  if (application.isError || !application.data) {
    return <ErrorNotice error={application.error} action="Could not load this application." />;
  }

  const app = application.data;
  const disabled = app.status === 'disabled';

  return (
    <>
      <div className="page__head">
        <div>
          <h1>{app.name}</h1>
          <div className="mono" style={{ color: 'var(--ink-muted)', marginTop: 2 }}>
            {app.slug} · {app.id}
          </div>
        </div>
        <div className="actions">
          <Link to={`/payments?application=${app.id}`} className="button button--small">
            View payments
          </Link>
          <button
            type="button"
            className={disabled ? 'button' : 'button button--danger'}
            onClick={() => update.mutate({ disabled: !disabled })}
            disabled={update.isPending}
          >
            {disabled ? 'Enable application' : 'Disable application'}
          </button>
        </div>
      </div>
      <p className="page__lede">
        {disabled
          ? 'This application is disabled. Its API keys are refused and no new payments can be created.'
          : app.description || 'No description.'}
      </p>

      {update.isError && <ErrorNotice error={update.error} action="That change was not saved." />}

      <ApiKeysPanel applicationId={applicationId} />
      <DestinationsPanel applicationId={applicationId} />
    </>
  );
}

function ApiKeysPanel({ applicationId }: { applicationId: string }) {
  const keys = useApiKeys(applicationId);
  const revoke = useRevokeApiKey(applicationId);
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<string | null>(null);

  return (
    <div className="panel">
      <div className="panel__head">
        <h2>API keys</h2>
        <button type="button" className="button button--small" onClick={() => setCreating(true)}>
          Create key
        </button>
      </div>

      {issued && (
        <div className="panel__body">
          <div className="notice notice--secret">
            <div>
              Copy this key now — it is shown once and cannot be retrieved afterwards.
            </div>
            <div className="notice__code">{issued}</div>
            <div>
              <button type="button" className="button button--small" onClick={() => setIssued(null)}>
                I've saved it
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="panel__scroll">
        {keys.isPending ? (
          <Loading rows={2} />
        ) : keys.data && keys.data.data.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>Key</th>
                <th>Name</th>
                <th>Mode</th>
                <th>Status</th>
                <th>Last used</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {keys.data.data.map((key) => (
                <tr key={key.id}>
                  <td data-label="Key" className="mono">{key.display_prefix}…</td>
                  <td data-label="Name">{key.name || '—'}</td>
                  <td data-label="Mode">
                    <Tag tone={key.mode === 'live' ? 'settled' : 'inert'}>{key.mode}</Tag>
                  </td>
                  <td data-label="Status">
                    <Tag tone={key.status === 'active' ? 'settled' : 'inert'}>{key.status}</Tag>
                  </td>
                  <td data-label="Last used">
                    <Timestamp value={key.last_used_at} />
                  </td>
                  <td>
                    {key.status === 'active' && (
                      <button
                        type="button"
                        className="button button--small button--danger"
                        onClick={() => revoke.mutate(key.id)}
                        disabled={revoke.isPending}
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty
            title="No API keys"
            hint="This application cannot call PayMux until it has one."
            action={
              <button type="button" className="button button--primary" onClick={() => setCreating(true)}>
                Create key
              </button>
            }
          />
        )}
      </div>

      {creating && (
        <CreateKeyDialog
          applicationId={applicationId}
          onClose={() => setCreating(false)}
          onIssued={(key) => {
            setIssued(key);
            setCreating(false);
          }}
        />
      )}
    </div>
  );
}

function CreateKeyDialog({
  applicationId,
  onClose,
  onIssued,
}: {
  applicationId: string;
  onClose: () => void;
  onIssued: (key: string) => void;
}) {
  const create = useCreateApiKey(applicationId);
  const [name, setName] = useState('');
  const [mode, setMode] = useState<'live' | 'test'>('test');

  const submit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(
      { name, mode },
      {
        onSuccess: (key) => {
          if (key.key) onIssued(key.key);
          else onClose();
        },
      },
    );
  };

  return (
    <Modal
      title="Create API key"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            form="create-key"
            className="button button--primary"
            disabled={create.isPending}
          >
            {create.isPending ? 'Creating…' : 'Create key'}
          </button>
        </>
      }
    >
      <form id="create-key" onSubmit={submit}>
        {create.isError && <ErrorNotice error={create.error} action="The key was not created." />}

        <div className="field">
          <label className="field__label" htmlFor="key-name">
            Name
          </label>
          <input
            id="key-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Production backend"
          />
          <span className="field__hint">For your own reference in this list.</span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="key-mode">
            Mode
          </label>
          <select
            id="key-mode"
            value={mode}
            onChange={(event) => setMode(event.target.value as 'live' | 'test')}
          >
            <option value="test">Test — for a sandbox gateway account</option>
            <option value="live">Live — for a production gateway account</option>
          </select>
          <span className="field__hint">
            PayMux refuses a mismatch, so a test key can never move real money.
          </span>
        </div>
      </form>
    </Modal>
  );
}

function DestinationsPanel({ applicationId }: { applicationId: string }) {
  const destinations = useDestinations(applicationId);
  const create = useCreateDestination(applicationId);
  const update = useUpdateDestination(applicationId);
  const rotate = useRotateDestinationSecret(applicationId);
  const remove = useDeleteDestination(applicationId);

  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState<string | null>(null);

  const add = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(
      { url },
      {
        onSuccess: (destination) => {
          setUrl('');
          if (destination.secret) setSecret(destination.secret);
        },
      },
    );
  };

  return (
    <div className="panel">
      <div className="panel__head">
        <h2>Webhook destinations</h2>
      </div>

      <div className="panel__body">
        {(create.isError || rotate.isError || remove.isError) && (
          <ErrorNotice
            error={create.error ?? rotate.error ?? remove.error}
            action="That change was not saved."
          />
        )}

        {secret && (
          <div className="notice notice--secret">
            <div>
              Copy this signing secret now — it is shown once. Your application needs it to verify
              the <span className="mono">PayMux-Signature</span> header.
            </div>
            <div className="notice__code">{secret}</div>
            <div>
              <button type="button" className="button button--small" onClick={() => setSecret(null)}>
                I've saved it
              </button>
            </div>
          </div>
        )}

        <form onSubmit={add} style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
          <input
            aria-label="Destination URL"
            className="mono"
            value={url}
            onChange={(event) => setUrl(event.target.value)}
            placeholder="https://product-b.example.com/webhooks/paymux"
          />
          <button type="submit" className="button button--primary" disabled={!url || create.isPending}>
            {create.isPending ? 'Adding…' : 'Add destination'}
          </button>
        </form>
      </div>

      <div className="panel__scroll">
        {destinations.isPending ? (
          <Loading rows={2} />
        ) : destinations.data && destinations.data.data.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th>URL</th>
                <th>Events</th>
                <th>State</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {destinations.data.data.map((destination) => (
                <tr key={destination.id}>
                  <td data-label="URL" className="mono cell--url">
                    {destination.url}
                  </td>
                  <td data-label="Events" className="gateway-status">
                    {destination.event_types.length === 0
                      ? 'all events'
                      : destination.event_types.join(', ')}
                  </td>
                  <td data-label="State">
                    <Tag tone={destination.enabled ? 'settled' : 'inert'}>
                      {destination.enabled ? 'enabled' : 'paused'}
                    </Tag>
                  </td>
                  <td>
                    <div className="actions">
                      <button
                        type="button"
                        className="button button--small"
                        onClick={() =>
                          update.mutate({ id: destination.id, enabled: !destination.enabled })
                        }
                        disabled={update.isPending}
                      >
                        {destination.enabled ? 'Pause' : 'Resume'}
                      </button>
                      <button
                        type="button"
                        className="button button--small"
                        onClick={() =>
                          rotate.mutate(destination.id, {
                            onSuccess: (updated) => updated.secret && setSecret(updated.secret),
                          })
                        }
                        disabled={rotate.isPending}
                      >
                        Rotate secret
                      </button>
                      <button
                        type="button"
                        className="button button--small button--danger"
                        onClick={() => remove.mutate(destination.id)}
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
            title="No destination"
            hint="Without one, PayMux records events for this application but has nowhere to deliver them."
          />
        )}
      </div>
    </div>
  );
}

/** Shown in the sidebar of a future detail view; kept exported for reuse. */
export function ApplicationSummary({ id, slug }: { id: string; slug: string }) {
  return (
    <dl className="kv">
      <KeyValue label="Application">
        <Id value={id} />
      </KeyValue>
      <KeyValue label="Slug">
        <span className="mono">{slug}</span>
      </KeyValue>
    </dl>
  );
}
