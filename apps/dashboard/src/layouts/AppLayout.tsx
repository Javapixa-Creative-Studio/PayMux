import { NavLink, Outlet } from 'react-router-dom';

import { useLogout, useOverview, useSession } from '../api/queries';
import { StatusStrip } from '../components/StatusStrip';

/**
 * Navigation is grouped by the question being asked, not by database table:
 * "what happened" (payments and the notifications behind them) is a different
 * job from "who is set up" (applications and gateways).
 */
const GROUPS = [
  {
    label: 'Operations',
    items: [
      { to: '/overview', label: 'Overview' },
      { to: '/payments', label: 'Payments' },
      { to: '/events', label: 'Events' },
      { to: '/deliveries', label: 'Deliveries', badge: 'deliveries' as const },
      { to: '/notifications', label: 'Notifications', badge: 'unrouted' as const },
    ],
  },
  {
    label: 'Configuration',
    items: [
      { to: '/applications', label: 'Applications' },
      { to: '/subscriptions', label: 'Subscriptions' },
      { to: '/gateways', label: 'Gateways' },
      { to: '/settings', label: 'Settings' },
    ],
  },
];

export function AppLayout() {
  const session = useSession();
  const logout = useLogout();
  const overview = useOverview('24h');

  // Counts only appear where something needs attention; a zero is not news.
  const badges = {
    deliveries: (overview.data?.deliveries.failed ?? 0) + (overview.data?.deliveries.dead ?? 0),
    unrouted: overview.data?.unrouted_notifications ?? 0,
  };

  return (
    <div className="shell">
      <aside className="rail">
        <div className="rail__brand">
          PayMux
          <span className="rail__env">midtrans</span>
        </div>

        {GROUPS.map((group) => (
          <div key={group.label}>
            <div className="rail__group">{group.label}</div>
            {group.items.map((item) => {
              const count = item.badge ? badges[item.badge] : 0;
              return (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    isActive ? 'rail__link rail__link--active' : 'rail__link'
                  }
                >
                  <span>{item.label}</span>
                  {count > 0 && <span className="rail__count">{count}</span>}
                </NavLink>
              );
            })}
          </div>
        ))}

        <div className="rail__footer">
          <div className="rail__account">{session.data?.email}</div>
          <button
            type="button"
            className="button button--small"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            {logout.isPending ? 'Signing out…' : 'Sign out'}
          </button>
        </div>
      </aside>

      <div className="main">
        <StatusStrip />
        <div className="page">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
