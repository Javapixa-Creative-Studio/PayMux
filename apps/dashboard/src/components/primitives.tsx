/**
 * The data primitives this interface is built from.
 *
 * Amounts, identifiers, timestamps and statuses are most of what an operator
 * reads here, so they get real components rather than ad-hoc markup, which is
 * what keeps a rupiah formatted the same way on every screen.
 */

import { useEffect, useState, type ReactNode } from 'react';

import type { DeliveryState, PaymentStatus, PayoutStatus, RoutingStatus } from '../api/types';
import {
  elideId,
  formatAmount,
  formatClock,
  formatRelative,
  formatTransportError,
} from '../lib/format';

export function Amount({ minor, currency }: { minor: number; currency: string }) {
  return (
    <span className="amount">
      <span className="amount__currency">{currency?.toUpperCase() || 'IDR'}</span>
      {formatAmount(minor, currency)}
    </span>
  );
}

/**
 * Renders a timestamp as elapsed time, with the exact value on hover.
 *
 * Operators nearly always want "how long ago", but when reconciling against a
 * gateway they need the precise instant, so both are available without a
 * second control.
 */
export function Timestamp({ value, absolute = false }: { value?: string | null; absolute?: boolean }) {
  if (!value) return <span className="timestamp">—</span>;

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return <span className="timestamp">—</span>;

  return (
    <time className="timestamp" dateTime={value} title={date.toISOString()}>
      {absolute ? formatClock(date) : formatRelative(date)}
    </time>
  );
}

/**
 * An identifier that copies itself when clicked.
 *
 * Identifiers here are 30-character ULIDs that operators paste into logs and
 * support threads, so copying is the action, and the middle is elided because
 * only the prefix and tail are ever read.
 */
export function Id({ value, full = false }: { value?: string; full?: boolean }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1200);
    return () => clearTimeout(timer);
  }, [copied]);

  if (!value) return <span className="id">—</span>;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      // Clipboard access can be denied; the value is still on screen.
    }
  };

  return (
    <button
      type="button"
      className={copied ? 'id id__copied' : 'id'}
      onClick={copy}
      title={copied ? 'Copied' : `${value}: click to copy`}
    >
      {copied ? 'copied' : full ? value : elideId(value)}
    </button>
  );
}

/** Which signal a payment status carries. */
function paymentTone(status: PaymentStatus): string {
  switch (status) {
    case 'PAID':
    case 'REFUNDED':
    case 'PARTIALLY_REFUNDED':
      return 'settled';
    case 'PENDING':
    case 'AUTHORIZED':
      return 'pending';
    case 'FAILED':
      return 'failed';
    default:
      return 'inert';
  }
}

export function PaymentStatusTag({ status }: { status: PaymentStatus }) {
  return <span className={`status status--${paymentTone(status)}`}>{status}</span>;
}

function deliveryTone(state: DeliveryState): string {
  switch (state) {
    case 'succeeded':
      return 'settled';
    case 'pending':
    case 'delivering':
      return 'pending';
    case 'failed':
    case 'dead':
      return 'failed';
    default:
      return 'inert';
  }
}

export function DeliveryStateTag({ state }: { state: DeliveryState }) {
  return <span className={`status status--${deliveryTone(state)}`}>{state}</span>;
}

function routingTone(status: RoutingStatus): string {
  switch (status) {
    case 'routed':
      return 'settled';
    case 'unrouted':
    case 'rejected':
      return 'failed';
    case 'duplicate':
    case 'ignored':
      return 'inert';
    default:
      return 'inert';
  }
}

export function RoutingTag({ status }: { status: RoutingStatus }) {
  return <span className={`status status--${routingTone(status)}`}>{status}</span>;
}

/** A pill for an arbitrary state, used where the tone is decided by the caller. */
export function Tag({ tone, children }: { tone: 'settled' | 'pending' | 'failed' | 'inert'; children: ReactNode }) {
  return <span className={`status status--${tone}`}>{children}</span>;
}

export function KeyValue({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  );
}

/**
 * An empty state that says what to do next.
 *
 * An empty screen in an ops tool usually means either "nothing has happened
 * yet" or "your filter excluded everything", and those need different answers.
 */
export function Empty({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty__title">{title}</div>
      {hint && <p className="empty__hint">{hint}</p>}
      {action && <div style={{ marginTop: 14 }}>{action}</div>}
    </div>
  );
}

export function Loading({ rows = 4 }: { rows?: number }) {
  return (
    <div style={{ padding: 14, display: 'grid', gap: 10 }} aria-busy="true" aria-live="polite">
      {Array.from({ length: rows }).map((_, index) => (
        <div key={index} className="skeleton" style={{ width: `${100 - index * 8}%` }} />
      ))}
    </div>
  );
}

/**
 * Renders an error in the interface's own voice: what failed, and the request
 * id an operator can quote when reporting it.
 */
export function ErrorNotice({ error, action }: { error: unknown; action?: string }) {
  const message = error instanceof Error ? error.message : 'Something went wrong.';
  const requestId =
    error && typeof error === 'object' && 'requestId' in error
      ? (error as { requestId?: string }).requestId
      : undefined;

  return (
    <div className="notice notice--error">
      <div>
        {action ? `${action} ` : ''}
        {message}
        {requestId && (
          <div className="mono" style={{ marginTop: 4, opacity: 0.8 }}>
            request {requestId}
          </div>
        )}
      </div>
    </div>
  );
}

export function CodeBlock({ value }: { value: unknown }) {
  return <pre className="code">{JSON.stringify(value, null, 2)}</pre>;
}

/**
 * The outcome of a delivery attempt: the status code when the destination
 * answered, the named cause when it never did.
 *
 * The full error stays on the title attribute. A table cell should say what
 * went wrong; the stack of detail belongs to whoever is already debugging it.
 */
export function DeliveryResult({
  statusCode,
  error,
}: {
  statusCode?: number;
  error?: string;
}) {
  if (statusCode) return <span className="gateway-status">HTTP {statusCode}</span>;
  if (!error) return <span className="gateway-status">—</span>;
  return (
    <span className="gateway-status" title={error}>
      {formatTransportError(error)}
    </span>
  );
}

/**
 * A payout's state, coloured by what it means for the money.
 *
 * UNRESOLVED gets the failure colour deliberately even though it is not a
 * failure. It means PayMux does not know whether the money left, which is the
 * state most deserving of an operator's attention: colouring it neutral
 * because it "might be fine" would bury the one row worth looking at.
 */
function payoutTone(status: PayoutStatus): string {
  switch (status) {
    case 'COMPLETED':
      return 'settled';
    case 'REQUESTED':
    case 'APPROVED':
    case 'SUBMITTED':
      return 'pending';
    case 'FAILED':
    case 'UNRESOLVED':
      return 'failed';
    default:
      return 'inert';
  }
}

export function PayoutStatusTag({ status }: { status: PayoutStatus }) {
  return <span className={`status status--${payoutTone(status)}`}>{status}</span>;
}
