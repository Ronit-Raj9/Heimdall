// Coupons. A store lists its coupons (see store.js); an order names at most
// one coupon code. A coupon applies on the order's business day when that
// day is inside its validFrom..validTo window (both ends included) and the
// subtotal reaches its minimum.

import { percentOf } from './money.js';

/** Coupon statuses reported on a priced order. */
export const CouponStatus = Object.freeze({
  APPLIED: 'applied',
  UNKNOWN: 'unknown',
  NOT_STARTED: 'not_started',
  EXPIRED: 'expired',
  BELOW_MINIMUM: 'below_minimum',
});

/** The store's coupon for code (case-insensitive), or undefined. */
export function findCoupon(store, code) {
  const wanted = String(code).trim().toUpperCase();
  return store.coupons.find((c) => c.code === wanted);
}

/**
 * What a coupon takes off a subtotal (cents) on a business day
 * ("YYYY-MM-DD"): { status, amount }. The amount is 0 unless the status is
 * "applied", and never more than the subtotal.
 */
export function couponDiscount(coupon, subtotal, day) {
  if (coupon.validFrom !== null && day < coupon.validFrom) return { status: CouponStatus.NOT_STARTED, amount: 0 };
  if (coupon.validTo !== null && day > coupon.validTo) return { status: CouponStatus.EXPIRED, amount: 0 };
  if (subtotal < coupon.minSubtotal) return { status: CouponStatus.BELOW_MINIMUM, amount: 0 };
  const amount = coupon.percentOff !== null ? percentOf(subtotal, coupon.percentOff) : coupon.amountOff;
  return { status: CouponStatus.APPLIED, amount: Math.min(amount, subtotal) };
}

/** Apply an order's coupon code, if any: { code, status, amount }. */
export function applyCoupon(store, code, subtotal, day) {
  if (code == null || code === '') return { code: null, status: null, amount: 0 };
  const coupon = findCoupon(store, code);
  if (!coupon) return { code: String(code).trim().toUpperCase(), status: CouponStatus.UNKNOWN, amount: 0 };
  return { code: coupon.code, ...couponDiscount(coupon, subtotal, day) };
}
