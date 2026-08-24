import { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';

import { useLogout, useOverview, usePayouts, useSession } from '../api/queries';
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
      { to: '/refunds', label: 'Refunds' },
      { to: '/payouts', label: 'Payouts', badge: 'payouts' as const },
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
      { to: '/integration', label: 'Integration' },
      { to: '/settings', label: 'Settings' },
    ],
  },
];

/**
 * The phone's tab bar, which is a different editorial decision from the rail.
 *
 * Nobody configures a gateway on a phone. They open PayMux on a phone because
 * something pinged them — a delivery is failing, a customer is asking whether a
 * payment went through — so the four tabs are the triage path and setup lives
 * behind More. Deliveries and Notifications keep their counts here for the same
 * reason the rail gives them counts: the tab bar is always on screen, so what
 * needs attention is visible without navigating to find it.
 */
const TABS = [
  { to: '/overview', label: 'Overview', glyph: '◎' },
  { to: '/payments', label: 'Payments', glyph: '⌗' },
  { to: '/deliveries', label: 'Deliveries', glyph: '⇄', badge: 'deliveries' as const },
  { to: '/notifications', label: 'Inbound', glyph: '↓', badge: 'unrouted' as const },
];

export function AppLayout() {
  const session = useSession();
  const logout = useLogout();
  const overview = useOverview('24h');
  const pendingPayouts = usePayouts({ status: 'REQUESTED', limit: 50 });
  const location = useLocation();
  const [moreOpen, setMoreOpen] = useState(false);

  // A sheet that survived navigation would cover the page it just opened.
  useEffect(() => setMoreOpen(false), [location.pathname]);

  // Escape closes it, the same as every other overlay in the console.
  useEffect(() => {
    if (!moreOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMoreOpen(false);
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [moreOpen]);

  // Counts only appear where something needs attention; a zero is not news.
  const badges = {
    deliveries: (overview.data?.deliveries.failed ?? 0) + (overview.data?.deliveries.dead ?? 0),
    unrouted: overview.data?.unrouted_notifications ?? 0,
    // A payout nobody releases is simply never paid, and it will not announce
    // itself. The count is the announcement.
    payouts: pendingPayouts.data?.data?.length ?? 0,
  };

  const onATab = TABS.some((tab) => location.pathname.startsWith(tab.to));

  // What needs attention behind More. Without this a payout awaiting approval
  // would be invisible on a phone: it is not one of the four tabs, and nobody
  // opens a drawer to check whether there is a reason to open the drawer.
  const tabbed = new Set(TABS.map((tab) => tab.to));
  const hidden = GROUPS.flatMap((group) => group.items)
    .filter((item) => item.badge && !tabbed.has(item.to))
    .reduce((n, item) => n + (item.badge ? badges[item.badge] : 0), 0);

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

      {/* Phone navigation. Hidden above the breakpoint, where the rail serves. */}
      <nav className="tabbar" aria-label="Sections">
        {TABS.map((tab) => {
          const count = tab.badge ? badges[tab.badge] : 0;
          return (
            <NavLink
              key={tab.to}
              to={tab.to}
              className={({ isActive }) => (isActive ? 'tab tab--active' : 'tab')}
            >
              <span className="tab__glyph" aria-hidden="true">
                {tab.glyph}
              </span>
              <span className="tab__label">{tab.label}</span>
              {count > 0 && (
                <span className="tab__count" aria-label={`${count} need attention`}>
                  {count > 99 ? '99+' : count}
                </span>
              )}
            </NavLink>
          );
        })}
        <button
          type="button"
          className={onATab ? 'tab' : 'tab tab--active'}
          aria-expanded={moreOpen}
          onClick={() => setMoreOpen((open) => !open)}
        >
          <span className="tab__glyph" aria-hidden="true">
            ≡
          </span>
          <span className="tab__label">More</span>
          {hidden > 0 && (
            <span className="tab__count" aria-label={`${hidden} need attention`}>
              {hidden > 99 ? '99+' : hidden}
            </span>
          )}
        </button>
      </nav>

      {moreOpen && (
        <div
          className="sheet__backdrop"
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) setMoreOpen(false);
          }}
        >
          <div className="sheet" role="dialog" aria-modal="true" aria-label="More">
            <div className="sheet__grip" aria-hidden="true" />
            {GROUPS.map((group) => (
              <div key={group.label} className="sheet__group">
                <div className="sheet__group-label">{group.label}</div>
                {group.items.map((item) => {
                  const count = item.badge ? badges[item.badge] : 0;
                  return (
                    <NavLink
                      key={item.to}
                      to={item.to}
                      className={({ isActive }) =>
                        isActive ? 'sheet__link sheet__link--active' : 'sheet__link'
                      }
                    >
                      <span>{item.label}</span>
                      {count > 0 && <span className="rail__count">{count}</span>}
                    </NavLink>
                  );
                })}
              </div>
            ))}

            <div className="sheet__footer">
              <div className="sheet__account">{session.data?.email}</div>
              <button
                type="button"
                className="button"
                onClick={() => logout.mutate()}
                disabled={logout.isPending}
              >
                {logout.isPending ? 'Signing out…' : 'Sign out'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
