// Rules 2 to 5: how multi-buy promotions price lines and orders.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { createCatalog, priceOrder, priceOrders } = acme;

const catalog = createCatalog([
  { sku: 'TEA-01', name: 'Green tea', price: '4.50' },
  { sku: 'MUG-01', name: 'Mug', price: '12.00' },
  { sku: 'POT-01', name: 'Teapot', price: '30.00' },
  { sku: 'FLT-01', name: 'Filters', price: '2.15' },
]);
const order = (lines, extra = {}) => ({
  id: 'P-1',
  placedAt: '2026-03-12T15:00:00Z',
  lines: lines.map(([sku, qty]) => ({ sku, qty })),
  ...extra,
});
const price = (lines, store, extra) => priceOrder(order(lines, extra), { catalog, store });

describe('multi-buy promotions', () => {
  test('free_units_per_complete_group', () => {
    const store = { promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }, { sku: 'FLT-01', buy: 3, get: 2 }] };
    const expectedFree = [0, 0, 1, 1, 1, 2, 2];
    for (let qty = 1; qty <= 7; qty++) {
      const [line] = price([['TEA-01', qty]], store).lines;
      const free = expectedFree[qty - 1];
      assert.equal(line.promo, free * 450, `TEA-01 x${qty}`);
      assert.equal(line.amount, qty * 450 - free * 450, `TEA-01 x${qty}`);
    }
    const filters = price([['FLT-01', 10], ['TEA-01', 3]], store).lines;
    assert.deepEqual(filters.map((l) => [l.promo, l.amount]), [[860, 1290], [450, 900]]);
    assert.equal(price([['FLT-01', 4]], store).lines[0].promo, 0);
  });

  test('units_counted_across_lines_free_from_last_line', () => {
    const buyOneGetOne = { promotions: [{ sku: 'TEA-01', buy: 1, get: 1 }] };
    const mixed = price([['TEA-01', 2], ['MUG-01', 1], ['TEA-01', 2], ['TEA-01', 1]], buyOneGetOne).lines;
    assert.deepEqual(mixed.map((l) => l.promo), [0, 0, 450, 450]);
    assert.deepEqual(mixed.map((l) => l.amount), [900, 1200, 450, 0]);
    const buyTwoGetOne = { promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }] };
    const split = price([['TEA-01', 2], ['TEA-01', 2], ['TEA-01', 2]], buyTwoGetOne).lines;
    assert.deepEqual(split.map((l) => l.promo), [0, 0, 900]);
    const spill = price([['TEA-01', 4], ['TEA-01', 1], ['TEA-01', 1]], buyOneGetOne).lines;
    assert.deepEqual(spill.map((l) => l.promo), [450, 450, 450]);
  });

  test('order_promo_and_subtotal_after_promotions', () => {
    const store = { promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }] };
    const p = price([['TEA-01', 3], ['MUG-01', 1]], store);
    assert.deepEqual(p.lines.map((l) => [l.sku, l.qty, l.unitPrice, l.promo, l.amount]), [
      ['TEA-01', 3, 450, 450, 900],
      ['MUG-01', 1, 1200, 0, 1200],
    ]);
    assert.equal(p.promo, 450);
    assert.equal(p.subtotal, 2100);
    assert.equal(p.total, 2100);
  });

  test('coupon_minimum_and_amount_use_subtotal_after_promotions', () => {
    const store = {
      promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }],
      coupons: [
        { code: 'WELCOME5', amountOff: '5.00', minSubtotal: '25.00' },
        { code: 'SAVE10', percentOff: '10' },
      ],
    };
    const small = price([['TEA-01', 6]], store, { coupon: 'WELCOME5' });
    assert.equal(small.subtotal, 1800);
    assert.deepEqual([small.coupon.status, small.discount, small.total], ['below_minimum', 0, 1800]);
    const percent = price([['TEA-01', 6], ['POT-01', 1]], store, { coupon: 'SAVE10' });
    assert.deepEqual([percent.subtotal, percent.discount, percent.total], [4800, 480, 4320]);
  });

  test('shipping_and_tax_use_subtotal_after_promotions', () => {
    const store = {
      taxRate: '10',
      shipping: { flat: '5.00', freeOver: '50.00' },
      promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }],
    };
    const under = price([['POT-01', 1], ['TEA-01', 6]], store);
    assert.deepEqual([under.subtotal, under.shipping, under.tax, under.total], [4800, 500, 480, 5780]);
    const over = price([['POT-01', 2], ['TEA-01', 3]], store);
    assert.deepEqual([over.subtotal, over.shipping, over.tax, over.total], [6900, 0, 690, 7590]);
    const batch = priceOrders([order([['POT-01', 1], ['TEA-01', 6]]), order([['POT-01', 2], ['TEA-01', 3]], { id: 'P-2' })], { catalog, store });
    assert.deepEqual(batch.map((p) => p.total), [5780, 7590]);
  });

  test('stores_without_promotions_unchanged', () => {
    const store = {
      taxRate: '8.875',
      shipping: { flat: '5.00', freeOver: '50.00' },
      coupons: [{ code: 'SPRING10', percentOff: '10', validFrom: '2026-03-01', validTo: '2026-03-31' }],
    };
    const plain = price([['POT-01', 2], ['TEA-01', 3]], store, { coupon: 'SPRING10' });
    assert.deepEqual(plain.lines.map((l) => [l.promo, l.amount]), [[0, 6000], [0, 1350]]);
    assert.deepEqual([plain.promo, plain.subtotal, plain.discount, plain.shipping, plain.tax, plain.total], [0, 7350, 735, 0, 587, 7202]);
    const otherSku = price([['TEA-01', 3]], { ...store, promotions: [{ sku: 'MUG-01', buy: 1, get: 1 }] });
    assert.deepEqual([otherSku.lines[0].promo, otherSku.lines[0].amount, otherSku.promo], [0, 1350, 0]);
  });
});
