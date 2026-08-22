import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';

import { ApiError } from '../api/client';
import { useLogin } from '../api/queries';

export function SignInPage() {
  const navigate = useNavigate();
  const login = useLogin();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    login.mutate(
      { email, password },
      { onSuccess: () => navigate('/overview', { replace: true }) },
    );
  };

  // Sign-in failures are deliberately undifferentiated by the API, so the
  // interface does not invent a distinction it was not told about.
  const message =
    login.error instanceof ApiError
      ? login.error.status === 429
        ? 'Too many attempts. Wait a moment and try again.'
        : login.error.message
      : login.error
        ? 'Could not reach PayMux. Check that the API is running.'
        : null;

  return (
    <div className="signin">
      <div className="signin__card">
        <div className="signin__brand">PayMux</div>
        <p className="signin__lede">Payment operations console</p>

        <form className="signin__form" onSubmit={onSubmit}>
          {message && <div className="notice notice--error">{message}</div>}

          <div className="field">
            <label className="field__label" htmlFor="email">
              Email
            </label>
            <input
              id="email"
              type="email"
              autoComplete="username"
              required
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </div>

          <div className="field">
            <label className="field__label" htmlFor="password">
              Password
            </label>
            <input
              id="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </div>

          <button
            type="submit"
            className="button button--primary"
            style={{ width: '100%' }}
            disabled={login.isPending}
          >
            {login.isPending ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  );
}
