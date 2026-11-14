// Rule 3: coupon windows are business days, both ends included.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { createCatalog, priceOrder } = acme;
const catalog = createCatalog([{ sku: 'POT-01', name: 'Teapot', price: '30.00' }]);
const order = (placedAt) => ({ id: 'C-1', placedAt, coupon: 'SPRING10', lines: [{ sku: 'POT-01', qty: 1 }] });

describe('coupon windows', () => {
  test('coupon_valid_through_last_local_day', () => {
    const store = { timeZone: 'America/New_York', coupons: [{ code: 'SPRING10', percentOff: '10', validFrom: '2026-03-01', validTo: '2026-03-31' }] };
    const lastEvening = priceOrder(order('2026-03-31T23:45:00-04:00'), { catalog, store });
    assert.deepEqual([lastEvening.coupon.status, lastEvening.discount], ['applied', 300]);
    const sameInstantUtc = priceOrder(order('2026-04-01T03:45:00Z'), { catalog, store });
    assert.deepEqual([sameInstantUtc.coupon.status, sameInstantUtc.discount], ['applied', 300]);
    const afterMidnight = priceOrder(order('2026-04-01T00:15:00-04:00'), { catalog, store });
    assert.deepEqual([afterMidnight.coupon.status, afterMidnight.discount], ['expired', 0]);
  });

  test('coupon_valid_from_first_local_day', () => {
    const store = { timeZone: 'Asia/Kolkata', coupons: [{ code: 'SPRING10', percentOff: '10', validFrom: '2026-04-01' }] };
    const firstMorning = priceOrder(order('2026-04-01T00:20:00+05:30'), { catalog, store });
    assert.deepEqual([firstMorning.coupon.status, firstMorning.discount], ['applied', 300]);
    const sameInstantUtc = priceOrder(order('2026-03-31T19:00:00Z'), { catalog, store });
    assert.equal(sameInstantUtc.coupon.status, 'applied');
    const dayBefore = priceOrder(order('2026-03-31T23:40:00+05:30'), { catalog, store });
    assert.deepEqual([dayBefore.coupon.status, dayBefore.discount], ['not_started', 0]);
  });
});
