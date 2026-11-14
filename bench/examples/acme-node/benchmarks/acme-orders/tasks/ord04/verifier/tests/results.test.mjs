// Rule 4: batching changes nothing in the results or the errors.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { createCatalog, priceOrder, priceOrders, OrderError } = acme;

const catalog = createCatalog([
  { sku: 'TEA-01', name: 'Green tea', price: '4.50' },
  { sku: 'TEA-02', name: 'Black tea', price: '7.25' },
  { sku: 'MUG-01', name: 'Mug', price: '12.00' },
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
const SKUS = ['TEA-01', 'TEA-02', 'MUG-01', 'POT-01'];
const COUPONS = [null, 'SPRING10', 'WELCOME5', 'NOPE'];
const orders = Array.from({ length: 60 }, (_, i) => ({
  id: `R-${i}`,
  customer: `c${i}@example.com`,
  placedAt: `2026-03-${String(10 + (i % 20)).padStart(2, '0')}T${String(i % 24).padStart(2, '0')}:15:00Z`,
  coupon: COUPONS[i % 4],
  lines: Array.from({ length: 1 + (i % 4) }, (_, j) => ({ sku: SKUS[(i + j) % 4], qty: 1 + ((i * j) % 5) })),
}));

describe('results', () => {
  test('results_match_one_by_one_pricing', () => {
    const batch = priceOrders(orders, { catalog, store });
    assert.deepEqual(batch, orders.map((o) => priceOrder(o, { catalog, store })));
    assert.deepEqual(batch.map((p) => p.orderId), orders.map((o) => o.id));
    const first = batch[1];
    assert.deepEqual(
      [first.businessDay, first.subtotal, first.coupon.status, first.discount, first.shipping, first.tax, first.total],
      ['2026-03-10', 3125, 'applied', 313, 500, 250, 3562],
    );
  });

  test('errors_match_one_by_one_pricing', () => {
    const ok = { id: 'A-1', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 1 }] };
    const unknown = { id: 'B-2', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 1 }, { sku: 'NOPE', qty: 1 }] };
    const badQty = { id: 'C-3', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'NOPE', qty: 0 }] };
    const fails = (message) => (err) => err instanceof OrderError && message.test(err.message);
    assert.throws(() => priceOrders([ok, unknown, badQty], { catalog, store }), fails(/^order B-2: unknown sku NOPE$/));
    assert.throws(() => priceOrders([ok, badQty, unknown], { catalog, store }), fails(/^order C-3: quantity/));
    assert.throws(() => priceOrder(unknown, { catalog, store }), fails(/^order B-2: unknown sku NOPE$/));
    assert.deepEqual(priceOrders([], { catalog, store }), []);
  });
});
