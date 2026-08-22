import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';

import {
  usePaymentAction,
  usePaymentDetail,
  useRefundPayment,
  useRetryDelivery,
} from '../api/queries';
import { Modal } from '../components/Modal';
import { Trace } from '../components/Trace';
import { formatAmount } from '../lib/format';
import {
  Amount,
  CodeBlock,
  DeliveryStateTag,
  ErrorNotice,
  Id,
  KeyValue,
  Loading,
  PaymentStatusTag,
  Timestamp,
} from '../components/primitives';

export function PaymentDetailPage() {
  const { paymentId = '' } = useParams();
  const detail = usePaymentDetail(paymentId);
  const action = usePaymentAction(paymentId);
  const retryDelivery = useRetryDelivery();
  const [refundOpen, setRefundOpen] = useState(false);

  if (detail.isPending) return <Loading rows={8} />;
  if (detail.isError || !detail.data) {
    return <ErrorNotice error={detail.error} action="Could not load this payment." />;
  }

  const { payment, events, deliveries, gateway_events: gatewayEvents, refunds } = detail.data;
  const canCancel = payment.status === 'PENDING' || payment.status === 'AUTHORIZED';
  const canRefund = payment.refundable_amount > 0;

  return (
    <>
      <div className="page__head">
        <div>
          <h1>Payment</h1>
          <div className="mono" style={{ color: 'var(--ink-muted)', marginTop: 2 }}>
            {payment.id}
          </div>
        </div>
        <Link to="/payments" className="button button--small">
          All payments
        </Link>
      </div>

      {action.isError && <ErrorNotice error={action.error} action="That action did not complete." />}

      <div className="summary" style={{ marginTop: 14 }}>
        <div>
          <div className="summary__amount">
            <Amount minor={payment.amount} currency={payment.currency} />
          </div>
          <div className="summary__meta">
            <PaymentStatusTag status={payment.status} />
            {payment.gateway_status && (
              <span className="gateway-status">
                {payment.gateway}: {payment.gateway_status}
              </span>
            )}
            {payment.payment_type && <span className="gateway-status">{payment.payment_type}</span>}
            {payment.refunded_amount > 0 && (
              <span className="gateway-status">
                refunded {formatAmount(payment.refunded_amount, payment.currency)}
              </span>
            )}
          </div>
        </div>

        <div className="actions">
          <button
            type="button"
            className="button"
            onClick={() => action.mutate('sync')}
            disabled={action.isPending}
          >
            {action.isPending && action.variables === 'sync' ? 'Syncing…' : 'Sync with gateway'}
          </button>
          {canCancel && (
            <>
              <button
                type="button"
                className="button"
                onClick={() => action.mutate('expire')}
                disabled={action.isPending}
              >
                Expire
              </button>
              <button
                type="button"
                className="button button--danger"
                onClick={() => action.mutate('cancel')}
                disabled={action.isPending}
              >
                Cancel
              </button>
            </>
          )}
          {canRefund && (
            <button type="button" className="button button--danger" onClick={() => setRefundOpen(true)}>
              Refund
            </button>
          )}
        </div>
      </div>

      <div className="detail">
        <div>
          {/* The trace is the point of this page: it is what tells an operator
              where a payment stopped, rather than only what state it is in. */}
          <div className="panel">
            <div className="panel__head">
              <h2>Trace</h2>
              <span className="gateway-status">gateway → paymux → application</span>
            </div>
            <div className="panel__body">
              <Trace
                payment={payment}
                events={events ?? []}
                deliveries={deliveries ?? []}
                gatewayEvents={gatewayEvents ?? []}
                refunds={refunds ?? []}
              />
            </div>
          </div>

          {deliveries && deliveries.length > 0 && (
            <div className="panel">
              <div className="panel__head">
                <h2>Deliveries</h2>
              </div>
              <div className="panel__scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Delivery</th>
                      <th>Event</th>
                      <th>State</th>
                      <th className="num">Attempts</th>
                      <th>Last result</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {deliveries.map((delivery) => (
                      <tr key={delivery.id}>
                        <td>
                          <Id value={delivery.id} />
                        </td>
                        <td>
                          <Id value={delivery.event_id} />
                        </td>
                        <td>
                          <DeliveryStateTag state={delivery.state} />
                        </td>
                        <td className="num">
                          {delivery.attempt_count}/{delivery.max_attempts}
                        </td>
                        <td className="gateway-status">
                          {delivery.last_status_code
                            ? `HTTP ${delivery.last_status_code}`
                            : delivery.last_error || '—'}
                        </td>
                        <td>
                          {delivery.state !== 'succeeded' && delivery.state !== 'delivering' && (
                            <button
                              type="button"
                              className="button button--small"
                              onClick={() => retryDelivery.mutate(delivery.id)}
                              disabled={retryDelivery.isPending}
                            >
                              Retry
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {refunds && refunds.length > 0 && (
            <div className="panel">
              <div className="panel__head">
                <h2>Refunds</h2>
              </div>
              <div className="panel__scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Refund</th>
                      <th className="num">Amount</th>
                      <th>Status</th>
                      <th>Reason</th>
                      <th>Created</th>
                    </tr>
                  </thead>
                  <tbody>
                    {refunds.map((refund) => (
                      <tr key={refund.id}>
                        <td>
                          <Id value={refund.id} />
                        </td>
                        <td className="num">
                          <Amount minor={refund.amount} currency={refund.currency} />
                        </td>
                        <td className="mono">{refund.status}</td>
                        <td>{refund.reason || refund.failure_reason || '—'}</td>
                        <td>
                          <Timestamp value={refund.created_at} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {gatewayEvents && gatewayEvents.length > 0 && (
            <div className="panel">
              <div className="panel__head">
                <h2>Raw gateway payload</h2>
                <span className="gateway-status">secrets removed before storage</span>
              </div>
              <div className="panel__body">
                <CodeBlock value={gatewayEvents[0].payload} />
              </div>
            </div>
          )}
        </div>

        <div>
          <div className="panel">
            <div className="panel__head">
              <h2>Details</h2>
            </div>
            <div className="panel__body">
              <dl className="kv">
                <KeyValue label="Application">
                  <Link to={`/applications/${payment.application_id}`}>
                    <Id value={payment.application_id} />
                  </Link>
                </KeyValue>
                <KeyValue label="Order reference">
                  <span className="mono">{payment.application_order_id}</span>
                </KeyValue>
                <KeyValue label="Gateway order">
                  <Id value={payment.gateway_order_id} />
                </KeyValue>
                <KeyValue label="Gateway transaction">
                  <Id value={payment.gateway_transaction_id} />
                </KeyValue>
                {payment.fraud_status && (
                  <KeyValue label="Fraud status">
                    <span className="mono">{payment.fraud_status}</span>
                  </KeyValue>
                )}
                <KeyValue label="Created">
                  <Timestamp value={payment.created_at} />
                </KeyValue>
                {payment.paid_at && (
                  <KeyValue label="Paid">
                    <Timestamp value={payment.paid_at} />
                  </KeyValue>
                )}
                {payment.expires_at && (
                  <KeyValue label="Expires">
                    <Timestamp value={payment.expires_at} />
                  </KeyValue>
                )}
                <KeyValue label="Refundable">
                  <Amount minor={payment.refundable_amount} currency={payment.currency} />
                </KeyValue>
              </dl>
            </div>
          </div>

          {payment.customer && (
            <div className="panel">
              <div className="panel__head">
                <h2>Customer</h2>
              </div>
              <div className="panel__body">
                <dl className="kv">
                  <KeyValue label="Name">
                    {[payment.customer.first_name, payment.customer.last_name]
                      .filter(Boolean)
                      .join(' ') || '—'}
                  </KeyValue>
                  <KeyValue label="Email">{payment.customer.email || '—'}</KeyValue>
                  <KeyValue label="Phone">{payment.customer.phone || '—'}</KeyValue>
                </dl>
              </div>
            </div>
          )}

          {payment.items && payment.items.length > 0 && (
            <div className="panel">
              <div className="panel__head">
                <h2>Items</h2>
              </div>
              <div className="panel__scroll">
                <table>
                  <tbody>
                    {payment.items.map((item, index) => (
                      <tr key={`${item.id}-${index}`}>
                        <td>{item.name}</td>
                        <td className="num">×{item.quantity}</td>
                        <td className="num">
                          <Amount minor={item.price} currency={payment.currency} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {Object.keys(payment.metadata ?? {}).length > 0 && (
            <div className="panel">
              <div className="panel__head">
                <h2>Metadata</h2>
              </div>
              <div className="panel__body">
                <CodeBlock value={payment.metadata} />
              </div>
            </div>
          )}
        </div>
      </div>

      {refundOpen && (
        <RefundDialog
          paymentId={paymentId}
          currency={payment.currency}
          refundable={payment.refundable_amount}
          onClose={() => setRefundOpen(false)}
        />
      )}
    </>
  );
}

function RefundDialog({
  paymentId,
  currency,
  refundable,
  onClose,
}: {
  paymentId: string;
  currency: string;
  refundable: number;
  onClose: () => void;
}) {
  const refund = useRefundPayment(paymentId);
  const [amount, setAmount] = useState(String(refundable));
  const [reason, setReason] = useState('');

  const parsed = Number(amount);
  const invalid = !Number.isInteger(parsed) || parsed <= 0 || parsed > refundable;

  return (
    <Modal
      title="Refund payment"
      onClose={onClose}
      footer={
        <>
          <button type="button" className="button" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="button button--primary"
            disabled={invalid || refund.isPending}
            onClick={() =>
              refund.mutate({ amount: parsed, reason }, { onSuccess: onClose })
            }
          >
            {refund.isPending ? 'Refunding…' : `Refund ${formatAmount(parsed || 0, currency)}`}
          </button>
        </>
      }
    >
      {refund.isError && <ErrorNotice error={refund.error} action="The refund was not accepted." />}

      <div className="field">
        <label className="field__label" htmlFor="refund-amount">
          Amount
        </label>
        <input
          id="refund-amount"
          className="mono"
          inputMode="numeric"
          value={amount}
          onChange={(event) => setAmount(event.target.value)}
        />
        <span className="field__hint">
          In {currency.toUpperCase()}. Up to {formatAmount(refundable, currency)} can still be
          refunded on this payment.
        </span>
        {invalid && amount !== '' && (
          <span className="field__error">
            Enter a whole amount between 1 and {formatAmount(refundable, currency)}.
          </span>
        )}
      </div>

      <div className="field">
        <label className="field__label" htmlFor="refund-reason">
          Reason
        </label>
        <input
          id="refund-reason"
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          placeholder="Shown to the gateway and stored with the refund"
        />
      </div>
    </Modal>
  );
}
