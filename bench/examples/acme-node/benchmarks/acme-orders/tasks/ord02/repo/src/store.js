// Store settings: time zone, tax rate, shipping and coupons. Settings arrive
// as plain JSON (store.json for the CLI) and are checked once by
// normalizeStore; pricing only ever sees the normalized form.

import { checkTimeZone, isIsoDate } from './dates.js';
import { parseMoney, parsePercent } from './money.js';
import { OrderError } from './errors.js';

const STORE_KEYS = new Set(['name', 'timeZone', 'taxRate', 'shipping', 'coupons']);
const normalized = new WeakSet();

/**
 * Check store settings and fill defaults: time zone "UTC", tax rate "0",
 * flat shipping 0 with no free-shipping threshold, no coupons. Calling it on
 * an already normalized store returns that store.
 */
export function normalizeStore(raw = {}) {
  if (normalized.has(raw)) return raw;
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) throw new OrderError('store: settings must be an object');
  for (const key of Object.keys(raw)) {
    if (!STORE_KEYS.has(key)) throw new OrderError(`store: unknown setting "${key}"`);
  }
  const store = {
    name: String(raw.name ?? 'store'),
    timeZone: field('timeZone', () => checkTimeZone(raw.timeZone ?? 'UTC')),
    taxRate: field('taxRate', () => parsePercent(raw.taxRate ?? '0')),
    shipping: field('shipping', () => ({
      flat: parseMoney(raw.shipping?.flat ?? 0),
      freeOver: raw.shipping?.freeOver == null ? null : parseMoney(raw.shipping.freeOver),
    })),
    coupons: (raw.coupons ?? []).map(normalizeCoupon),
  };
  const codes = new Set();
  for (const c of store.coupons) {
    if (codes.has(c.code)) throw new OrderError(`store: duplicate coupon ${c.code}`);
    codes.add(c.code);
  }
  Object.freeze(store);
  normalized.add(store);
  return store;
}

function normalizeCoupon(raw, i) {
  const where = `store: coupon ${i + 1}`;
  if (typeof raw?.code !== 'string' || raw.code.trim() === '') throw new OrderError(`${where}: code is required`);
  const hasPercent = raw.percentOff != null;
  if (hasPercent === (raw.amountOff != null)) throw new OrderError(`${where}: set exactly one of percentOff and amountOff`);
  for (const key of ['validFrom', 'validTo']) {
    if (raw[key] != null && !isIsoDate(raw[key])) throw new OrderError(`${where}: ${key} must be YYYY-MM-DD`);
  }
  return Object.freeze({
    code: raw.code.trim().toUpperCase(),
    percentOff: hasPercent ? field(where, () => parsePercent(raw.percentOff)) : null,
    amountOff: hasPercent ? null : field(where, () => parseMoney(raw.amountOff)),
    minSubtotal: field(where, () => parseMoney(raw.minSubtotal ?? 0)),
    validFrom: raw.validFrom ?? null,
    validTo: raw.validTo ?? null,
  });
}

function field(name, read) {
  try {
    return read();
  } catch (err) {
    if (err instanceof OrderError) throw err;
    throw new OrderError(`${name.startsWith('store:') ? name : `store: ${name}`}: ${err.message}`);
  }
}
