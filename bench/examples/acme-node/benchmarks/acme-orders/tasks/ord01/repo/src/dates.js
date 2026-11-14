// Timestamps and business days. Orders carry an ISO 8601 timestamp with an
// explicit offset ("2026-03-31T20:15:00-04:00" or "...Z"). A store reports
// by business day: the calendar date in the store's IANA time zone.

const TIMESTAMP_RE =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?(Z|[+-]\d{2}:\d{2})$/;
const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

const dayFormatters = new Map();

/**
 * Parse an ISO 8601 timestamp with a "Z" or "+HH:MM"/"-HH:MM" suffix into a
 * Date. A Date is returned as a copy. Anything else is a RangeError.
 */
export function parseTimestamp(value) {
  if (value instanceof Date) {
    if (Number.isNaN(value.getTime())) throw new RangeError('invalid timestamp: Invalid Date');
    return new Date(value.getTime());
  }
  const m = TIMESTAMP_RE.exec(String(value).trim());
  if (!m) throw new RangeError(`invalid timestamp "${value}"`);
  const [, year, month, day, hour, minute, second = '0', fraction = '0'] = m;
  if (!isCalendarDate(+year, +month, +day) || +hour > 23 || +minute > 59 || +second > 59) {
    throw new RangeError(`invalid timestamp "${value}"`);
  }
  const millis = Date.UTC(+year, +month - 1, +day, +hour, +minute, +second, +fraction.padEnd(3, '0'));
  return new Date(millis);
}

/**
 * The calendar date ("YYYY-MM-DD") of an instant in an IANA time zone,
 * daylight saving time included. `instant` is a Date or a timestamp string.
 */
export function businessDay(instant, timeZone = 'UTC') {
  const date = parseTimestamp(instant);
  const parts = {};
  for (const part of dayFormatter(timeZone).formatToParts(date)) parts[part.type] = part.value;
  return `${parts.year}-${parts.month}-${parts.day}`;
}

/** Throw a RangeError unless timeZone is a time zone this runtime knows. */
export function checkTimeZone(timeZone) {
  dayFormatter(timeZone);
  return timeZone;
}

/** True for a real calendar date written "YYYY-MM-DD". */
export function isIsoDate(text) {
  const m = DATE_RE.exec(String(text));
  return m !== null && isCalendarDate(+m[1], +m[2], +m[3]);
}

function isCalendarDate(year, month, day) {
  const d = new Date(Date.UTC(year, month - 1, day));
  return d.getUTCFullYear() === year && d.getUTCMonth() === month - 1 && d.getUTCDate() === day;
}

function dayFormatter(timeZone) {
  let f = dayFormatters.get(timeZone);
  if (!f) {
    try {
      f = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' });
    } catch {
      throw new RangeError(`unknown time zone "${timeZone}"`);
    }
    dayFormatters.set(timeZone, f);
  }
  return f;
}
