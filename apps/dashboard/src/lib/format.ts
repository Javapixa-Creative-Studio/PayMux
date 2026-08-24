/**
 * Formatting helpers shared across the console.
 *
 * These live apart from the components that use them so money and time are
 * rendered the same way everywhere, and so the component modules stay
 * component-only.
 */

/** Currencies with no minor unit, where the stored integer is already whole. */
const ZERO_DECIMAL = new Set(['IDR', 'JPY', 'KRW', 'VND']);

/**
 * Formats an amount held in the currency's minor unit.
 *
 * PayMux stores money as an integer — rupiah for IDR, cents for USD — so the
 * exponent has to be reapplied here rather than assumed.
 */
export function formatAmount(minor: number, currency: string): string {
  const code = currency?.toUpperCase() || 'IDR';
  const fractionDigits = ZERO_DECIMAL.has(code) ? 0 : 2;
  const value = fractionDigits === 0 ? minor : minor / 100;
  return new Intl.NumberFormat('en-US', {
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(value);
}

/**
 * Renders a moment relative to now, in the operator's terms.
 *
 * Both directions matter here: a delivery's next attempt is in the future, and
 * rendering that as "just now" would tell an operator the retry has already
 * happened when it has not.
 */
export function formatRelative(date: Date, now: number = Date.now()): string {
  const deltaSeconds = Math.round((now - date.getTime()) / 1000);
  const future = deltaSeconds < 0;
  const seconds = Math.abs(deltaSeconds);

  const phrase = (value: string) => (future ? `in ${value}` : `${value} ago`);

  if (seconds < 5) return future ? 'in a moment' : 'just now';
  if (seconds < 60) return phrase(`${seconds}s`);
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return phrase(`${minutes}m`);
  const hours = Math.round(minutes / 60);
  if (hours < 24) return phrase(`${hours}h`);
  const days = Math.round(hours / 24);
  if (days < 30) return phrase(`${days}d`);
  return date.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
}

/**
 * Renders a wall-clock time, for reconciling against a gateway's records.
 *
 * Always 24-hour: an operator comparing PayMux against a gateway's log should
 * never have to work out whether 11:12 was morning or night.
 */
export function formatClock(date: Date): string {
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  });
}

/**
 * Shortens an identifier for a table cell.
 *
 * PayMux identifiers are 30-character ULIDs; only the prefix and the tail are
 * ever read by eye, and the full value is always a click away.
 */
export function elideId(value: string): string {
  if (value.length <= 18) return value;
  return `${value.slice(0, 11)}…${value.slice(-4)}`;
}

/**
 * Reduces a transport error to the part an operator can act on.
 *
 * Go's errors are built for logs, not for table cells: they lead with the
 * method and full URL, which the neighbouring Destination column already
 * shows, and bury the cause at the end. This keeps the cause. The untouched
 * string stays available on hover, because the detail matters once you are
 * actually debugging.
 */
export function formatTransportError(value: string): string {
  // Strip Go's `Post "https://…":` / `Get "https://…":` prefix.
  const stripped = value.replace(/^[A-Z][a-z]+ "[^"]*":\s*/, '').trim();

  for (const [pattern, phrase] of TRANSPORT_CAUSES) {
    if (pattern.test(stripped)) return phrase;
  }
  return stripped.length > 64 ? `${stripped.slice(0, 63)}…` : stripped;
}

/**
 * Causes worth naming, most specific first. Everything else falls through to
 * the raw text — inventing a friendly phrase for an error we have not seen
 * would hide the one detail that explains it.
 */
const TRANSPORT_CAUSES: Array<[RegExp, string]> = [
  [/connection refused/i, 'connection refused'],
  [/no such host|name resolution|dns/i, 'host not found'],
  [/connection reset/i, 'connection reset'],
  [/context deadline exceeded|Client\.Timeout|i\/o timeout|timeout/i, 'timed out'],
  [/x509|certificate|tls/i, 'TLS rejected'],
  [/blocked|private|loopback|not permitted/i, 'destination not allowed'],
];
