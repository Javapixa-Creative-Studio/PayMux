import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import type { Overview } from '../api/types';
import { FanOut } from './FanOut';

type App = NonNullable<Overview['applications']>[number];

function app(name: string, overrides: Partial<App> = {}): App {
  return {
    application_id: `app_${name}`,
    name,
    payments: 0,
    paid: 0,
    pending: 0,
    deliveries_ok: 0,
    deliveries_failed: 0,
    deliveries_dead: 0,
    ...overrides,
  };
}

function overview(apps: App[], unrouted = 0): Overview {
  return {
    window: '24h',
    payments: { created: 9, paid: 7, pending: 2, failed: 0 },
    deliveries: { pending: 0, failed: 0, succeeded: 0, dead: 0 },
    unrouted_notifications: unrouted,
    currency_totals: null,
    applications: apps,
  };
}

/**
 * Returns a scope limited to the diagram itself. The footer repeats some of the
 * same counts, so an unscoped query cannot tell a branch label from a summary.
 */
function renderFanOut(data: Overview) {
  const { container } = render(
    <MemoryRouter>
      <FanOut overview={data} />
    </MemoryRouter>,
  );
  return within(container.querySelector('svg') as unknown as HTMLElement);
}

describe('FanOut', () => {
  it('names the branch that is broken rather than reporting an aggregate', () => {
    // The whole point of the schematic: healthy applications must not hide the
    // one that is failing.
    const diagram = renderFanOut(
      overview([
        app('Product A', { payments: 4, paid: 4, deliveries_ok: 4 }),
        app('Product B', { payments: 3, paid: 3, deliveries_ok: 2, deliveries_dead: 1 }),
      ]),
    );

    expect(diagram.getByText('Product B')).toBeInTheDocument();
    expect(diagram.getByText('1 delivery gave up')).toBeInTheDocument();
    expect(diagram.getByText('4 events delivered')).toBeInTheDocument();
    expect(screen.getByText('1 application not receiving events')).toBeInTheDocument();
  });

  it('describes a branch by what needs acting on, not by what is most numerous', () => {
    // 40 successful deliveries do not make one that gave up less urgent.
    const diagram = renderFanOut(
      overview([app('Busy', { payments: 40, paid: 40, deliveries_ok: 40, deliveries_dead: 1 })]),
    );

    expect(diagram.getByText('1 delivery gave up')).toBeInTheDocument();
    expect(diagram.queryByText('40 events delivered')).not.toBeInTheDocument();
  });

  it('reports unattributed notifications separately from failing branches', () => {
    // A red gateway wire and a caption saying everything is fine would
    // contradict each other.
    const diagram = renderFanOut(overview([app('Product A', { payments: 2, deliveries_ok: 2 })], 3));

    expect(screen.getByText(/3 notifications matched no payment/)).toBeInTheDocument();
    expect(diagram.getByText('3 unattributed')).toBeInTheDocument();
  });

  it('says so plainly when nothing is wrong', () => {
    renderFanOut(overview([app('Product A', { payments: 2, paid: 2, deliveries_ok: 2 })]));

    expect(screen.getByText('every application is receiving its events')).toBeInTheDocument();
    expect(screen.queryByText(/Open deliveries/)).not.toBeInTheDocument();
  });

  it('summarises the applications it cannot draw instead of cropping them', () => {
    const diagram = renderFanOut(overview(Array.from({ length: 9 }, (_, i) => app(`App ${i + 1}`))));

    expect(diagram.getByText('App 6')).toBeInTheDocument();
    expect(diagram.queryByText('App 7')).not.toBeInTheDocument();
    expect(diagram.getByText('and 3 more')).toBeInTheDocument();
  });

  it('handles an install with no applications yet', () => {
    const diagram = renderFanOut(overview([]));

    expect(diagram.getByText('no applications yet')).toBeInTheDocument();
  });
});
