// Rules 1 to 3: the options form, the positional form and formatCents give
// the same results. Deprecation warnings are checked in warnings.test.mjs,
// each in a fresh process.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { createCatalog, formatCents, formatMoney, priceOrder, priceOrders, OrderError } = acme;

const catalog = createCatalog([
  { sku: 'TEA-01', name: 'Green tea', price: '4.50' },
  { sku: 'POT-01', name: 'Teapot', price: '29.90' },
]);
const store = {
  timeZone: 'America/New_York',
  taxRate: '8.875',
  shipping: { flat: '5.00', freeOver: '50.00' },
  coupons: [{ code: 'SPRING10', percentOff: '10', validFrom: '2026-03-01', validTo: '2026-03-31' }],
};
const orders = [
  { id: 'R-1', placedAt: '2026-03-12T15:00:00Z', coupon: 'SPRING10', lines: [{ sku: 'POT-01', qty: 2 }, { sku: 'TEA-01', qty: 1 }] },
  { id: 'R-2', placedAt: '2026-03-13T02:00:00Z', lines: [{ sku: 'TEA-01', qty: 3 }] },
];

describe('api', () => {
  test('options_form_prices_like_positional', () => {
    for (const order of orders) {
      const withStore = priceOrder(order, { catalog, store });
      assert.deepEqual(priceOrder(order, catalog, store), withStore);
      assert.deepEqual(priceOrder(order, { catalog }), priceOrder(order, catalog));
    }
    assert.equal(priceOrder(orders[0], { catalog, store }).total, 6301);
    assert.equal(priceOrder(orders[1], { catalog, store }).businessDay, '2026-03-12');
    assert.deepEqual(priceOrders(orders, { catalog, store }), priceOrders(orders, catalog, store));
    assert.deepEqual(priceOrders(orders, { catalog }), priceOrders(orders, catalog));
    const bad = { id: 'R-3', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'GONE', qty: 1 }] };
    assert.throws(() => priceOrder(bad, { catalog, store }), (e) => e instanceof OrderError && e.message === 'order R-3: unknown sku GONE');
    assert.throws(() => priceOrder(bad, catalog, store), (e) => e instanceof OrderError && e.message === 'order R-3: unknown sku GONE');
  });

  test('options_form_requires_catalog', () => {
    const order = orders[1];
    assert.throws(() => priceOrder(order, {}), TypeError);
    assert.throws(() => priceOrder(order, { store }), TypeError);
    assert.throws(() => priceOrder(order, { catalog: {} }), TypeError);
    assert.throws(() => priceOrder(order), TypeError);
    assert.throws(() => priceOrders([order], { store }), TypeError);
  });

  test('format_cents_replaces_format_money', () => {
    for (const [cents, text] of [[1234, '12.34'], [5, '0.05'], [-50, '-0.50'], [0, '0.00'], [100000, '1000.00']]) {
      assert.equal(formatCents(cents), text);
      assert.equal(formatMoney(cents), text);
    }
    assert.throws(() => formatCents(1.5), TypeError);
  });
});
