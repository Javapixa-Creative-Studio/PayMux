import { useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';

import { useApplications, useCreateApplication } from '../api/queries';
import { Modal } from '../components/Modal';
import { Empty, ErrorNotice, Loading, Tag, Timestamp } from '../components/primitives';

export function ApplicationsPage() {
  const applications = useApplications({ limit: 100 });
  const [creating, setCreating] = useState(false);

  return (
    <>
      <div className="page__head">
        <h1>Applications</h1>
        <button type="button" className="button button--primary" onClick={() => setCreating(true)}>
          New application
        </button>
      </div>
      <p className="page__lede">
        Each application is a separate tenant: its own API keys, its own webhook destination, and no
        access to any other application's payments.
      </p>

      {applications.isError && (
        <ErrorNotice error={applications.error} action="Could not load applications." />
      )}

      <div className="panel">
        <div className="panel__scroll">
          {applications.isPending ? (
            <Loading rows={4} />
          ) : applications.data && applications.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Slug</th>
                  <th>Status</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {applications.data.data.map((app) => (
                  <tr key={app.id}>
                    <td data-label="Name" data-primary="">
                      <Link to={`/applications/${app.id}`}>{app.name}</Link>
                    </td>
                    <td data-label="Slug" className="mono">{app.slug}</td>
                    <td data-label="Status">
                      <Tag tone={app.status === 'active' ? 'settled' : 'inert'}>{app.status}</Tag>
                    </td>
                    <td data-label="Created">
                      <Timestamp value={app.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty
              title="No applications yet"
              hint="Create one for each product that needs to take payments through this gateway account."
              action={
                <button type="button" className="button button--primary" onClick={() => setCreating(true)}>
                  New application
                </button>
              }
            />
          )}
        </div>
      </div>

      {creating && <CreateApplicationDialog onClose={() => setCreating(false)} />}
    </>
  );
}

function CreateApplicationDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateApplication();
  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [description, setDescription] = useState('');

  const submit = (event: FormEvent) => {
    event.preventDefault();
    create.mutate(
      { name, slug: slug || undefined, description: description || undefined },
      { onSuccess: onClose },
    );
  };

  return (
    <Modal
      title="New application"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            form="create-application"
            className="button button--primary"
            disabled={!name || create.isPending}
          >
            {create.isPending ? 'Creating…' : 'Create application'}
          </button>
        </>
      }
    >
      <form id="create-application" onSubmit={submit}>
        {create.isError && (
          <ErrorNotice error={create.error} action="The application was not created." />
        )}

        <div className="field">
          <label className="field__label" htmlFor="app-name">
            Name
          </label>
          <input
            id="app-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Product B"
            required
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="app-slug">
            Slug
          </label>
          <input
            id="app-slug"
            className="mono"
            value={slug}
            onChange={(event) => setSlug(event.target.value)}
            placeholder="product-b"
          />
          <span className="field__hint">
            Lowercase letters, digits and hyphens. Derived from the name if you leave it empty.
          </span>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="app-description">
            Description
          </label>
          <input
            id="app-description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            placeholder="What this application is for"
          />
        </div>
      </form>
    </Modal>
  );
}
