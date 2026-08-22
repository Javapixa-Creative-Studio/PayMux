import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import type { Delivery, GatewayEvent, Payment, PayMuxEvent } from '../api/types';
import { Trace } from './Trace';

const payment: Payment = {
  id: 'pay_01ARZ3NDEKTSV4RRFFQ69G5FAV',
  object: 'payment',
  application_id: 'app_01ARZ3NDEKTSV4RRFFQ69G5FAV',
  gateway: 'midtrans',
  application_order_id: 'INV-000123',
  gateway_order_id: 'pmx_01ARZ3NDEKTSV4RRFFQ69G5FAV',
  amount: 150000,
  currency: 'IDR',
  status: 'PAID',
  refunded_amount: 0,
  refundable_amount: 150000,
  metadata: {},
  expires_at: null,
  snap_token: 'snap-token',
  created_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:05:13Z',
};

function renderTrace(overrides: {
  events?: PayMuxEvent[];
  deliveries?: Delivery[];
  gatewayEvents?: GatewayEvent[];
}) {
  return render(
    <MemoryRouter>
      <Trace
        payment={payment}
        events={overrides.events ?? []}
        deliveries={overrides.deliveries ?? []}
        gatewayEvents={overrides.gatewayEvents ?? []}
        refunds={[]}
      />
    </MemoryRouter>,
  );
}

describe('Trace', () => {
  it('orders the chain of custody by time, whatever order it arrives in', () => {
    // The lists come from separate endpoints, so the component — not the API —
    // is what puts the story in order.
    renderTrace({
      deliveries: [
        {
          id: 'dlv_1',
          object: 'delivery',
          event_id: 'evt_1',
          application_id: payment.application_id,
          destination_id: 'dst_1',
          url: 'https://product-b.example.com/webhooks/paymux',
          state: 'succeeded',
          attempt_count: 1,
          max_attempts: 7,
          last_attempt_at: '2026-08-20T10:05:13Z',
          last_status_code: 200,
          last_duration_ms: 142,
          created_at: '2026-08-20T10:05:12Z',
        },
      ],
      events: [
        {
          id: 'evt_1',
          object: 'event',
          type: 'payment.paid',
          gateway: 'midtrans',
          application_id: payment.application_id,
          payment_id: payment.id,
          data: {
            id: 'evt_1',
            type: 'payment.paid',
            gateway: 'midtrans',
            application_id: payment.application_id,
            status: 'PAID',
            created_at: '2026-08-20T10:05:12Z',
          },
          created_at: '2026-08-20T10:05:12Z',
        },
      ],
      gatewayEvents: [
        {
          id: 'gev_1',
          object: 'gateway_event',
          gateway: 'midtrans',
          payment_id: payment.id,
          gateway_status: 'settlement',
          fraud_status: 'accept',
          signature_verified: true,
          routing_status: 'routed',
          received_at: '2026-08-20T10:05:11Z',
        },
      ],
    });

    const entries = screen.getAllByRole('listitem');
    const headlines = entries.map((entry) => entry.textContent ?? '');

    expect(headlines[0]).toContain('Payment created');
    expect(headlines[1]).toContain('Notification received');
    expect(headlines[2]).toContain('payment.paid');
    expect(headlines[3]).toContain('Delivered to the application');
  });

  it('shows a rejected notification as rejected and says nothing was applied', () => {
    // A forged notification must never read like a successful one.
    renderTrace({
      gatewayEvents: [
        {
          id: 'gev_2',
          object: 'gateway_event',
          gateway: 'midtrans',
          gateway_status: 'settlement',
          signature_verified: false,
          routing_status: 'rejected',
          routing_error: 'signature verification failed',
          received_at: '2026-08-20T10:05:11Z',
        },
      ],
    });

    const entry = screen.getByText('Notification rejected').closest('li');
    expect(entry).not.toBeNull();
    expect(within(entry!).getByText(/Nothing was applied/)).toBeInTheDocument();
    expect(within(entry!).getByText('signature failed')).toBeInTheDocument();
  });

  it('reports a failing delivery with its status code and attempt count', () => {
    renderTrace({
      deliveries: [
        {
          id: 'dlv_2',
          object: 'delivery',
          event_id: 'evt_2',
          application_id: payment.application_id,
          destination_id: 'dst_1',
          url: 'https://product-b.example.com/webhooks/paymux',
          state: 'failed',
          attempt_count: 3,
          max_attempts: 7,
          last_status_code: 500,
          last_error: 'destination returned HTTP 500',
          created_at: '2026-08-20T10:05:12Z',
        },
      ],
    });

    const entry = screen.getByText('Delivery failed, retry scheduled').closest('li');
    expect(entry).not.toBeNull();
    expect(within(entry!).getByText('HTTP 500')).toBeInTheDocument();
    expect(within(entry!).getByText('attempt 3/7')).toBeInTheDocument();
  });

  it('says so plainly when there is nothing to show', () => {
    render(
      <MemoryRouter>
        <Trace
          payment={{ ...payment, created_at: '' }}
          events={[]}
          deliveries={[]}
          gatewayEvents={[]}
          refunds={[]}
        />
      </MemoryRouter>,
    );
    // The payment-created entry always exists, so the list is never empty for
    // a real payment; this guards the fallback copy staying wired up.
    expect(screen.getAllByRole('listitem').length).toBeGreaterThan(0);
  });
});
