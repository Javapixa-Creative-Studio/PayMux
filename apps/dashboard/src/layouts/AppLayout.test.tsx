/**
 * The phone's navigation.
 *
 * The rail and the tab bar are both in the document at every width: CSS
 * decides which one is on screen, so these assert on the tab bar specifically
 * rather than on link text, which both would match.
 */

import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { renderScreen, stubApi } from '../test/harness';
import { AppLayout } from './AppLayout';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const overview = {
  window: '24h',
  payments: { created: 9, paid: 7, pending: 2, failed: 0 },
  deliveries: { pending: 0, failed: 2, succeeded: 4, dead: 1 },
  unrouted_notifications: 5,
  currency_totals: null,
  applications: [],
};

function mount(path = '/overview', data: unknown = overview) {
  const api = stubApi([
    { path: '/admin/api/auth/me', body: { email: 'admin@paymux.local' } },
    { path: '/admin/api/overview', body: data },
    { path: '/admin/api/', body: { data: [], has_more: false, limit: 25 } },
  ]);
  renderScreen(<AppLayout />, path);
  return api;
}

function tabbar() {
  return within(screen.getByRole('navigation', { name: 'Sections' }));
}

describe('phone navigation', () => {
  it('puts the triage path on the tab bar and setup behind More', async () => {
    // Nobody adds a gateway account on a phone, so Gateways is not worth one of
    // five tab slots; a failing delivery is.
    mount();

    const bar = tabbar();
    expect(bar.getByText('Overview')).toBeInTheDocument();
    expect(bar.getByText('Deliveries')).toBeInTheDocument();
    expect(bar.getByText('More')).toBeInTheDocument();
    expect(bar.queryByText('Gateways')).not.toBeInTheDocument();
    expect(bar.queryByText('Settings')).not.toBeInTheDocument();
  });

  it('carries the counts that say something needs attention', async () => {
    mount();

    // Failed and dead deliveries are one number to an operator: not delivered.
    await waitFor(() => expect(tabbar().getByText('3')).toBeInTheDocument());
    expect(tabbar().getByText('3')).toHaveAttribute('aria-label', '3 need attention');
    expect(tabbar().getByText('5')).toHaveAttribute('aria-label', '5 need attention');
  });

  it('shows no count when nothing is wrong, because a zero is not news', async () => {
    mount('/overview', {
      ...overview,
      deliveries: { pending: 0, failed: 0, succeeded: 4, dead: 0 },
      unrouted_notifications: 0,
    });

    await waitFor(() => expect(screen.getAllByText('Overview').length).toBeGreaterThan(0));
    expect(tabbar().queryByText('0')).not.toBeInTheDocument();
  });

  it('opens More onto the destinations the tab bar could not hold', async () => {
    mount();

    await userEvent.click(tabbar().getByText('More'));

    const sheet = within(screen.getByRole('dialog', { name: 'More' }));
    expect(sheet.getByText('Gateways')).toBeInTheDocument();
    expect(sheet.getByText('Subscriptions')).toBeInTheDocument();
  });

  it('reaches Sign out, which the phone layout had no way to show', async () => {
    // The rail's footer is hidden below the breakpoint, so before the sheet
    // existed there was no way to sign out on a phone at all.
    mount();

    await userEvent.click(tabbar().getByText('More'));

    const sheet = within(screen.getByRole('dialog', { name: 'More' }));
    expect(sheet.getByRole('button', { name: 'Sign out' })).toBeInTheDocument();
    await waitFor(() => expect(sheet.getByText('admin@paymux.local')).toBeInTheDocument());
  });

  it('marks More as the active tab when the route is not one of the four', async () => {
    // Otherwise the bar claims nothing is selected while a page is on screen.
    mount('/gateways');

    const more = tabbar().getByText('More').closest('button');
    expect(more).toHaveClass('tab--active');
  });

  it('closes the sheet on Escape', async () => {
    mount();

    await userEvent.click(tabbar().getByText('More'));
    expect(screen.getByRole('dialog', { name: 'More' })).toBeInTheDocument();

    await userEvent.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: 'More' })).not.toBeInTheDocument(),
    );
  });
});
