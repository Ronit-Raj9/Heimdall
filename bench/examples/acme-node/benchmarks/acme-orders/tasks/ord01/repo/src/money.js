// Money helpers. Money is an integer number of cents everywhere in this
// package; text such as "12.50" is converted at the edges (catalog files,
// store settings, CLI output). Percentages are decimal strings with at most
// three decimal places ("8.875") and are applied exactly, without floats.

const MONEY_RE = /^(\d+)(?:\.(\d{1,2}))?$/;
const PERCENT_RE = /^(\d{1,3})(?:\.(\d{1,3}))?$/;

/**
 * Parse a non-negative money amount ("12", "12.5", "12.34") into cents.
 * An integer is taken as cents already.
 */
export function parseMoney(value) {
  if (Number.isInteger(value) && value >= 0) return value;
  const m = MONEY_RE.exec(String(value).trim());
  if (!m) throw new RangeError(`invalid money amount "${value}"`);
  return Number(m[1]) * 100 + Number((m[2] ?? '').padEnd(2, '0'));
}

/** Format cents as text with two decimals: 1234 -> "12.34", -50 -> "-0.50". */
export function formatCents(cents) {
  if (!Number.isInteger(cents)) throw new TypeError(`cents must be an integer, got ${cents}`);
  const sign = cents < 0 ? '-' : '';
  const abs = Math.abs(cents);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`;
}

/** Parse a percentage ("8.875", "10") into thousandths of a percent. */
export function parsePercent(value) {
  const m = PERCENT_RE.exec(String(value).trim());
  if (!m) throw new RangeError(`invalid percentage "${value}"`);
  const milli = Number(m[1]) * 1000 + Number((m[2] ?? '').padEnd(3, '0'));
  if (milli > 100000) throw new RangeError(`percentage over 100: "${value}"`);
  return milli;
}

/**
 * The given percentage of an amount in cents, rounded half up to a whole
 * cent. `percent` is a percentage string or thousandths from parsePercent.
 */
export function percentOf(cents, percent) {
  const milli = typeof percent === 'number' ? percent : parsePercent(percent);
  const numerator = cents * milli;
  const denominator = 100000;
  return Math.floor((2 * numerator + denominator) / (2 * denominator));
}
