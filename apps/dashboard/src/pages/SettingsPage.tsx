import { useState, type FormEvent } from 'react';

import { useAdmins, useChangePassword, useCreateAdmin, useSession } from '../api/queries';
import { Modal } from '../components/Modal';
import { Empty, ErrorNotice, Loading, Tag, Timestamp } from '../components/primitives';

export function SettingsPage() {
  const session = useSession();
  const admins = useAdmins();
  const [inviting, setInviting] = useState(false);

  return (
    <>
      <div className="page__head">
        <h1>Settings</h1>
      </div>
      <p className="page__lede">Administrators who can sign in to this console.</p>

      <ChangePasswordPanel />

      <div className="panel">
        <div className="panel__head">
          <h2>Administrators</h2>
          <button type="button" className="button button--small" onClick={() => setInviting(true)}>
            Add administrator
          </button>
        </div>
        <div className="panel__scroll">
          {admins.isPending ? (
            <Loading rows={2} />
          ) : admins.data && admins.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Email</th>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Last signed in</th>
                </tr>
              </thead>
              <tbody>
                {admins.data.data.map((admin) => (
                  <tr key={admin.id}>
                    <td>
                      {admin.email}
                      {admin.id === session.data?.id && (
                        <span className="gateway-status" style={{ marginLeft: 8 }}>
                          you
                        </span>
                      )}
                    </td>
                    <td>{admin.name || '—'}</td>
                    <td>
                      <Tag tone={admin.status === 'active' ? 'settled' : 'inert'}>{admin.status}</Tag>
                    </td>
                    <td>
                      <Timestamp value={admin.last_login_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty title="No administrators" />
          )}
        </div>
      </div>

      {inviting && <AddAdminDialog onClose={() => setInviting(false)} />}
    </>
  );
}

function ChangePasswordPanel() {
  const change = useChangePassword();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');

  const submit = (event: FormEvent) => {
    event.preventDefault();
    change.mutate(
      { current_password: current, new_password: next },
      {
        onSuccess: () => {
          setCurrent('');
          setNext('');
          // Every session was revoked, including this one.
          window.location.assign('/signin');
        },
      },
    );
  };

  return (
    <div className="panel">
      <div className="panel__head">
        <h2>Your password</h2>
      </div>
      <div className="panel__body">
        {change.isError && (
          <ErrorNotice error={change.error} action="The password was not changed." />
        )}

        <form onSubmit={submit} style={{ maxWidth: 380 }}>
          <div className="field">
            <label className="field__label" htmlFor="current-password">
              Current password
            </label>
            <input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={current}
              onChange={(event) => setCurrent(event.target.value)}
              required
            />
          </div>

          <div className="field">
            <label className="field__label" htmlFor="new-password">
              New password
            </label>
            <input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(event) => setNext(event.target.value)}
              minLength={12}
              required
            />
            <span className="field__hint">
              At least 12 characters. Changing it signs you out everywhere, including here.
            </span>
          </div>

          <button
            type="submit"
            className="button button--primary"
            disabled={change.isPending || next.length < 12}
          >
            {change.isPending ? 'Changing…' : 'Change password'}
          </button>
        </form>
      </div>
    </div>
  );
}

function AddAdminDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateAdmin();
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');

  const submit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate({ email, name, password }, { onSuccess: onClose });
  };

  return (
    <Modal
      title="Add administrator"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            form="add-admin"
            className="button button--primary"
            disabled={create.isPending || password.length < 12}
          >
            {create.isPending ? 'Adding…' : 'Add administrator'}
          </button>
        </>
      }
    >
      <form id="add-admin" onSubmit={submit}>
        {create.isError && (
          <ErrorNotice error={create.error} action="The administrator was not added." />
        )}

        <div className="field">
          <label className="field__label" htmlFor="admin-email">
            Email
          </label>
          <input
            id="admin-email"
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            required
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="admin-name">
            Name
          </label>
          <input id="admin-name" value={name} onChange={(event) => setName(event.target.value)} />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="admin-password">
            Initial password
          </label>
          <input
            id="admin-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            minLength={12}
            required
          />
          <span className="field__hint">
            At least 12 characters. Share it securely and ask them to change it after signing in.
          </span>
        </div>
      </form>
    </Modal>
  );
}
