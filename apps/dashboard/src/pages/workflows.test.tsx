/**
 * The dashboard workflows PRD §87 names: signing in, creating an application,
 * listing payments, reading a payment's detail, configuring a gateway and
 * retrying a delivery.
 *
 * These assert on what the operator sees and on the request the page sends,
 * which is where a filter or scoping bug would actually show up.
 */

import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { page, renderScreen, stubApi } from '../test/harness';
import { ApplicationsPage } from './ApplicationsPage';
import { DeliveriesPage } from './DeliveriesPage';
import { GatewaysPage } from './GatewaysPage';
import { PaymentDetailPage } from './PaymentDetailPage';
import { PaymentsPage } from './PaymentsPage';
import { SignInPage } from './SignInPage';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const application = {
  id: 'app_01ARZ3NDEKTSV4RRFFQ69G5FAV',
  name: 'Product B',
  slug: 'product-b',
  description: '',
  status: 'active' as const,
  metadata: {},
  created_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:00:00Z',
};

const payment = {
  id: 'pay_01ARZ3NDEKTSV4RRFFQ69G5FAV',
  object: 'payment' as const,
  application_id: application.id,
  gateway: 'midtrans',
  application_order_id: 'INV-000123',
  gateway_order_id: 'pmx_01ARZ3NDEKTSV4RRFFQ69G5FAV',
  amount: 150000,
  currency: 'IDR',
  status: 'PAID' as const,
  gateway_status: 'settlement',
  refunded_amount: 0,
  refundable_amount: 150000,
  metadata: {},
  expires_at: null,
  created_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:05:00Z',
};

describe('signing in', () => {
  it('sends the credentials and reports a rejection without inventing detail', async () => {
    const api = stubApi([
      {
        method: 'POST',
        path: '/admin/api/auth/login',
        status: 401,
        body: { error: { code: 'UNAUTHORIZED', message: 'Email or password is incorrect.' } },
      },
    ]);

    renderScreen(<SignInPage />);

    await userEvent.type(screen.getByLabelText('Email'), 'admin@paymux.test');
    await userEvent.type(screen.getByLabelText('Password'), 'not-the-password');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    // The API deliberately does not say which half was wrong, and neither
    // should the interface.
    expect(await screen.findByText('Email or password is incorrect.')).toBeInTheDocument();

    const [request] = api.to('/admin/api/auth/login');
    expect(request.body).toEqual({ email: 'admin@paymux.test', password: 'not-the-password' });
  });
});

describe('creating an application', () => {
  it('posts the new application and closes the dialog', async () => {
    const api = stubApi([
      { method: 'GET', path: '/admin/api/applications', body: page([]) },
      { method: 'POST', path: '/admin/api/applications', status: 201, body: application },
    ]);

    renderScreen(<ApplicationsPage />);

    await userEvent.click(await screen.findByRole('button', { name: 'New application' }));
    await userEvent.type(screen.getByLabelText('Name'), 'Product B');
    await userEvent.click(screen.getByRole('button', { name: 'Create application' }));

    await waitFor(() => {
      const created = api.requests.find(
        (request) => request.method === 'POST' && request.url.startsWith('/admin/api/applications'),
      );
      expect(created?.body).toMatchObject({ name: 'Product B' });
    });

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('says what to do next when there are no applications', async () => {
    stubApi([{ method: 'GET', path: '/admin/api/applications', body: page([]) }]);
    renderScreen(<ApplicationsPage />);

    expect(await screen.findByText('No applications yet')).toBeInTheDocument();
    expect(screen.getByText(/Create one for each product/)).toBeInTheDocument();
  });
});

describe('listing payments', () => {
  it('shows the amount in the currency major unit and passes filters to the API', async () => {
    const api = stubApi([
      { method: 'GET', path: '/admin/api/applications', body: page([application]) },
      { method: 'GET', path: '/admin/api/payments', body: page([payment]) },
    ]);

    renderScreen(<PaymentsPage />);

    // 150000 minor units of IDR is Rp 150.000, not Rp 1.500.
    expect(await screen.findByText('150,000')).toBeInTheDocument();

    // Scoped to the table: the status filter offers "PAID" as an option too.
    const row = screen.getByText('INV-000123').closest('tr');
    expect(row).not.toBeNull();
    expect(within(row!).getByText('PAID')).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByLabelText('Status'), 'PENDING');

    await waitFor(() => {
      expect(api.to('/admin/api/payments').some((request) => request.url.includes('status=PENDING')))
        .toBe(true);
    });
  });

  it('distinguishes an empty result from an empty filter', async () => {
    stubApi([
      { method: 'GET', path: '/admin/api/applications', body: page([application]) },
      { method: 'GET', path: '/admin/api/payments', body: page([]) },
    ]);

    renderScreen(<PaymentsPage />);
    expect(await screen.findByText('No payments yet')).toBeInTheDocument();
  });
});

describe('reading a payment', () => {
  it('renders the trace and offers only the actions the payment allows', async () => {
    stubApi([
      {
        method: 'GET',
        path: '/admin/api/payments/',
        body: {
          payment,
          refunds: [],
          events: [
            {
              id: 'evt_1',
              object: 'event',
              type: 'payment.paid',
              gateway: 'midtrans',
              application_id: application.id,
              payment_id: payment.id,
              data: {
                id: 'evt_1',
                type: 'payment.paid',
                gateway: 'midtrans',
                application_id: application.id,
                status: 'PAID',
                created_at: '2026-08-20T10:05:00Z',
              },
              created_at: '2026-08-20T10:05:00Z',
            },
          ],
          deliveries: [],
          gateway_events: [],
        },
      },
    ]);

    renderScreen(<PaymentDetailPage />, `/payments/${payment.id}`, '/payments/:paymentId');

    expect(await screen.findByRole('heading', { name: 'Trace' })).toBeInTheDocument();
    expect(screen.getByText('payment.paid')).toBeInTheDocument();

    // A settled payment can be refunded but no longer cancelled, and the
    // buttons have to agree with that.
    expect(screen.getByRole('button', { name: 'Refund' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument();
  });

  it('caps a refund at the refundable balance', async () => {
    stubApi([
      {
        method: 'GET',
        path: '/admin/api/payments/',
        body: {
          payment: { ...payment, refunded_amount: 100000, refundable_amount: 50000 },
          refunds: [],
          events: [],
          deliveries: [],
          gateway_events: [],
        },
      },
    ]);

    renderScreen(<PaymentDetailPage />, `/payments/${payment.id}`, '/payments/:paymentId');
    await userEvent.click(await screen.findByRole('button', { name: 'Refund' }));

    const amount = screen.getByLabelText('Amount');
    await userEvent.clear(amount);
    await userEvent.type(amount, '90000');

    // Over-refunding is refused by the API too, but the interface should not
    // let an operator submit a request that cannot succeed.
    expect(await screen.findByText(/Enter a whole amount between 1 and 50,000/)).toBeInTheDocument();
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByRole('button', { name: /^Refund/ })).toBeDisabled();
  });
});

describe('configuring a gateway', () => {
  it('reports the outcome of a connection test', async () => {
    const account = {
      id: 'gwa_1',
      gateway: 'midtrans',
      name: 'Midtrans sandbox',
      environment: 'sandbox' as const,
      merchant_id: 'G123456789',
      client_key: 'SB-Mid-client-abc',
      server_key_set: true,
      enabled: true,
      is_default: true,
      capabilities: {
        checkout: true,
        refund: true,
        partial_refund: true,
        subscriptions: false,
        cancel: true,
        expire: true,
      },
      last_checked_at: null,
      last_check_ok: null,
      created_at: '2026-08-20T10:00:00Z',
      updated_at: '2026-08-20T10:00:00Z',
    };

    const tested = {
      ...account,
      last_checked_at: '2026-08-20T11:00:00Z',
      last_check_ok: false,
      last_check_error:
        'The gateway rejected these credentials. Check the server key, and that it belongs to the environment selected here.',
    };
    // The page refetches the list after a test, so the stub tracks the change
    // the same way the server would.
    let checked = false;

    stubApi([
      {
        method: 'GET',
        path: '/admin/api/gateways/accounts',
        body: () => page([checked ? tested : account]),
      },
      { method: 'GET', path: '/admin/api/gateways', body: page([{ name: 'midtrans' }]) },
      {
        method: 'POST',
        path: '/admin/api/gateways/accounts/gwa_1/test',
        body: () => {
          checked = true;
          return tested;
        },
      },
    ]);

    renderScreen(<GatewaysPage />);

    expect(await screen.findByText('not tested')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Test connection' }));

    // A failed check has to keep the gateway's explanation: "rejected" and
    // "unreachable" call for different responses.
    expect(await screen.findByText('failed')).toBeInTheDocument();
    expect(screen.getByText(/Check the server key/)).toBeInTheDocument();
  });

  it('never offers to reveal a stored server key', async () => {
    stubApi([
      { method: 'GET', path: '/admin/api/gateways/accounts', body: page([]) },
      { method: 'GET', path: '/admin/api/gateways', body: page([{ name: 'midtrans' }]) },
    ]);

    renderScreen(<GatewaysPage />);
    expect(await screen.findByText('No gateway account')).toBeInTheDocument();

    // Both the header and the empty state offer this; either opens the dialog.
    await userEvent.click(screen.getAllByRole('button', { name: 'Add account' })[0]);
    // The field is write-only and masked; there is no read path for it.
    expect(screen.getByLabelText('Server key')).toHaveAttribute('type', 'password');
  });
});

describe('retrying a delivery', () => {
  it('asks the API to requeue and offers no retry for one already delivered', async () => {
    const failed = {
      id: 'dlv_failed',
      object: 'delivery' as const,
      event_id: 'evt_1',
      application_id: application.id,
      destination_id: 'dst_1',
      url: 'https://product-b.example.com/webhooks/paymux',
      state: 'failed' as const,
      attempt_count: 3,
      max_attempts: 7,
      next_attempt_at: '2026-08-20T11:00:00Z',
      last_status_code: 500,
      created_at: '2026-08-20T10:00:00Z',
    };
    const succeeded = { ...failed, id: 'dlv_ok', state: 'succeeded' as const, last_status_code: 200 };

    const api = stubApi([
      { method: 'GET', path: '/admin/api/deliveries', body: page([failed, succeeded]) },
      { method: 'POST', path: '/admin/api/deliveries/dlv_failed/retry', body: { ...failed, state: 'pending' } },
    ]);

    renderScreen(<DeliveriesPage />);

    const retryButtons = await screen.findAllByRole('button', { name: 'Retry now' });
    // Only the failed delivery is retryable; a delivered one is finished.
    expect(retryButtons).toHaveLength(1);

    await userEvent.click(retryButtons[0]);
    await waitFor(() => {
      expect(api.to('/admin/api/deliveries/dlv_failed/retry')).toHaveLength(1);
    });
  });
});
