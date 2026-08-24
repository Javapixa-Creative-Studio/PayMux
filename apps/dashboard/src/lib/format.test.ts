import { describe, expect, it } from 'vitest';

import { elideId, formatAmount, formatRelative, formatTransportError } from './format';

describe('formatAmount', () => {
  it('renders zero-decimal currencies as whole units', () => {
    // The API stores rupiah as an integer, so 150000 is Rp 150.000, not
    // Rp 1.500,00. Getting this wrong misstates every amount by a hundredfold.
    expect(formatAmount(150000, 'IDR')).toBe('150,000');
    expect(formatAmount(0, 'IDR')).toBe('0');
    expect(formatAmount(1, 'JPY')).toBe('1');
  });

  it('reapplies the exponent for currencies with a minor unit', () => {
    expect(formatAmount(1050, 'USD')).toBe('10.50');
    expect(formatAmount(5, 'USD')).toBe('0.05');
    expect(formatAmount(100000, 'SGD')).toBe('1,000.00');
  });

  it('normalises the currency code and defaults to IDR', () => {
    expect(formatAmount(1050, 'usd')).toBe('10.50');
    expect(formatAmount(150000, '')).toBe('150,000');
  });
});

describe('formatRelative', () => {
  const now = new Date('2026-08-20T12:00:00Z').getTime();
  const ago = (seconds: number) => new Date(now - seconds * 1000);

  it('describes recent moments the way an operator reads them', () => {
    expect(formatRelative(ago(1), now)).toBe('just now');
    expect(formatRelative(ago(30), now)).toBe('30s ago');
    expect(formatRelative(ago(90), now)).toBe('2m ago');
    expect(formatRelative(ago(3600 * 3), now)).toBe('3h ago');
    expect(formatRelative(ago(86400 * 2), now)).toBe('2d ago');
  });

  it('reads forwards for a time that has not arrived yet', () => {
    // A delivery's next attempt is in the future; calling that "just now"
    // would say the retry had already happened.
    const ahead = (seconds: number) => new Date(now + seconds * 1000);
    expect(formatRelative(ahead(2), now)).toBe('in a moment');
    expect(formatRelative(ahead(45), now)).toBe('in 45s');
    expect(formatRelative(ahead(720), now)).toBe('in 12m');
    expect(formatRelative(ahead(3600 * 6), now)).toBe('in 6h');
  });

  it('falls back to a date once relative time stops being useful', () => {
    const old = formatRelative(ago(86400 * 200), now);
    expect(old).not.toMatch(/ago/);
  });
});

describe('elideId', () => {
  it('keeps the prefix and tail of a ULID', () => {
    const id = 'pay_01ARZ3NDEKTSV4RRFFQ69G5FAV';
    const elided = elideId(id);
    expect(elided.startsWith('pay_01ARZ3N')).toBe(true);
    expect(elided.endsWith('G5FAV'.slice(-4))).toBe(true);
    expect(elided.length).toBeLessThan(id.length);
  });

  it('leaves short identifiers alone', () => {
    expect(elideId('app_123')).toBe('app_123');
  });
});

describe('formatTransportError', () => {
  it('drops the method and URL Go prefixes onto every transport error', () => {
    // The Destination column already shows the URL; repeating it pushes the
    // cause off the end of the cell.
    expect(
      formatTransportError(
        'Post "http://127.0.0.1:9911/webhooks/paymux": dial tcp 127.0.0.1:9911: connect: connection refused',
      ),
    ).toBe('connection refused');
  });

  it('names the causes an operator has a different fix for', () => {
    expect(formatTransportError('dial tcp: lookup nope.example.com: no such host')).toBe(
      'host not found',
    );
    expect(formatTransportError('context deadline exceeded (Client.Timeout exceeded)')).toBe(
      'timed out',
    );
    expect(
      formatTransportError('x509: certificate signed by unknown authority'),
    ).toBe('TLS rejected');
  });

  it('keeps an unrecognised error rather than inventing a friendly phrase', () => {
    // A cause we have never seen is exactly the one worth reading verbatim.
    expect(formatTransportError('Post "https://a.example/x": something entirely new')).toBe(
      'something entirely new',
    );
  });

  it('truncates a long unrecognised error instead of breaking the table', () => {
    const long = `Post "https://a.example/x": ${'z'.repeat(200)}`;
    const out = formatTransportError(long);
    expect(out).toHaveLength(64);
    expect(out.endsWith('…')).toBe(true);
  });
});
