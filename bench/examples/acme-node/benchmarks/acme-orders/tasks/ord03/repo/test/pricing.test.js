import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createCatalog, OrderError, priceOrder, priceOrders } from '../src/index.js';

const catalog = createCatalog([
  { sku: 'TEA-01', name: 'Green tea', price: '4.50' },
  { sku: 'POT-01', name: 'Teapot', price: '29.90' },
]);
const store = {
  timeZone: 'America/New_York',
  taxRate: '8.875',
  shipping: { flat: '5.00', freeOver: '50.00' },
  coupons: [
    { code: 'SPRING10', percentOff: '10', validFrom: '2026-03-01', validTo: '2026-03-31' },
    { code: 'WELCOME5', amountOff: '5.00', minSubtotal: '25.00' },
  ],
};
const order = (lines, extra = {}) => ({ id: 'A-1', placedAt: '2026-03-12T15:00:00Z', lines, ...extra });

test('prices lines, shipping and tax', () => {
  const p = priceOrder(order([{ sku: 'TEA-01', qty: 2 }]), catalog, store);
  assert.equal(p.lines[0].amount, 900);
  assert.equal(p.subtotal, 900);
  assert.equal(p.shipping, 500);
  assert.equal(p.tax, 80);
  assert.equal(p.total, 1480);
  assert.equal(p.businessDay, '2026-03-12');
});

test('tax is charged after the coupon discount and shipping is free over the threshold', () => {
  const p = priceOrder(order([{ sku: 'POT-01', qty: 2 }], { coupon: 'spring10' }), catalog, store);
  assert.deepEqual(p.coupon, { code: 'SPRING10', status: 'applied' });
  assert.equal(p.discount, 598);
  assert.equal(p.shipping, 0);
  assert.equal(p.tax, 478);
  assert.equal(p.total, 5980 - 598 + 478);
});

test('coupon outside its window or below its minimum takes nothing', () => {
  const late = priceOrder(order([{ sku: 'POT-01', qty: 1 }], { coupon: 'SPRING10', placedAt: '2026-04-15T15:00:00Z' }), catalog, store);
  assert.equal(late.coupon.status, 'expired');
  assert.equal(late.discount, 0);
  const small = priceOrder(order([{ sku: 'TEA-01', qty: 1 }], { coupon: 'WELCOME5' }), catalog, store);
  assert.equal(small.coupon.status, 'below_minimum');
  const unknown = priceOrder(order([{ sku: 'TEA-01', qty: 1 }], { coupon: 'NOPE' }), catalog, store);
  assert.equal(unknown.coupon.status, 'unknown');
});

test('store settings default to UTC with no tax', () => {
  const p = priceOrder(order([{ sku: 'TEA-01', qty: 1 }]), catalog);
  assert.equal(p.tax, 0);
  assert.equal(p.shipping, 0);
  assert.equal(p.total, 450);
});

test('bad orders are refused', () => {
  assert.throws(() => priceOrder(order([{ sku: 'GONE', qty: 1 }]), catalog), /unknown sku GONE/);
  assert.throws(() => priceOrder(order([{ sku: 'TEA-01', qty: 0 }]), catalog), OrderError);
  assert.throws(() => priceOrder(order([]), catalog), OrderError);
  assert.throws(() => priceOrder(order([{ sku: 'TEA-01', qty: 1 }]), {}), TypeError);
});

test('priceOrders keeps input order', () => {
  const out = priceOrders([order([{ sku: 'TEA-01', qty: 1 }], { id: 'B' }), order([{ sku: 'POT-01', qty: 1 }], { id: 'A' })], catalog, store);
  assert.deepEqual(out.map((p) => p.orderId), ['B', 'A']);
});
