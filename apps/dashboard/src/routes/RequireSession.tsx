import { Navigate, Outlet } from 'react-router-dom';

import { ApiError } from '../api/client';
import { useSession } from '../api/queries';
import { Loading } from '../components/primitives';

/**
 * Gates the console behind a session.
 *
 * The check is a real request rather than a cached flag: the session lives in
 * an HttpOnly cookie the page cannot read, so asking the API is the only
 * honest way to know whether the operator is still signed in.
 */
export function RequireSession() {
  const session = useSession();

  if (session.isPending) {
    return (
      <div style={{ padding: 32, maxWidth: 420 }}>
        <Loading rows={3} />
      </div>
    );
  }

  const unauthenticated = session.error instanceof ApiError && session.error.isUnauthorized;
  if (unauthenticated || (session.isError && !session.data)) {
    return <Navigate to="/signin" replace />;
  }

  return <Outlet />;
}
