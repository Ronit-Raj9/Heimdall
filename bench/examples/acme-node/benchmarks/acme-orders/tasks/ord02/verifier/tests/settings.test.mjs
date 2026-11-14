// Rule 1: promotion settings are checked wherever store settings are read.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { createCatalog, normalizeStore, parseStore, priceOrder, priceOrders, ImportError, OrderError } = acme;
const catalog = createCatalog([{ sku: 'TEA-01', name: 'Green tea', price: '4.50' }]);
const order = { id: 'S-1', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 3 }] };

const INVALID = {
  'buy is zero': [{ sku: 'TEA-01', buy: 0, get: 1 }],
  'get is not an integer': [{ sku: 'TEA-01', buy: 2, get: 1.5 }],
  'buy is text': [{ sku: 'TEA-01', buy: '2', get: 1 }],
  'get is missing': [{ sku: 'TEA-01', buy: 2 }],
  'sku is missing': [{ buy: 2, get: 1 }],
  'two promotions for one sku': [{ sku: 'TEA-01', buy: 2, get: 1 }, { sku: 'TEA-01', buy: 3, get: 1 }],
  'not a list': { sku: 'TEA-01', buy: 2, get: 1 },
};
const refused = (cls) => (err) => err instanceof cls && /promotion/.test(err.message);

describe('promotion settings', () => {
  test('invalid_promotions_refused', () => {
    for (const [why, promotions] of Object.entries(INVALID)) {
      const store = { timeZone: 'UTC', promotions };
      assert.throws(() => normalizeStore(store), refused(OrderError), `normalizeStore: ${why}`);
      assert.throws(() => priceOrder(order, { catalog, store }), refused(OrderError), `priceOrder: ${why}`);
      assert.throws(() => priceOrders([order], { catalog, store }), refused(OrderError), `priceOrders: ${why}`);
      assert.throws(() => parseStore(JSON.stringify(store)), refused(ImportError), `parseStore: ${why}`);
    }
  });

  test('valid_promotions_accepted', () => {
    const text = JSON.stringify({ promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }, { sku: 'NOT-SOLD', buy: 5, get: 5 }] });
    const store = parseStore(text);
    assert.equal(priceOrder(order, { catalog, store }).lines[0].promo, 450);
    assert.equal(priceOrder(order, { catalog, store: JSON.parse(text) }).promo, 450);
  });
});
