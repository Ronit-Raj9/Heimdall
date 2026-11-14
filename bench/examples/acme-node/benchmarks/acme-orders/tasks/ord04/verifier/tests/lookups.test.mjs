// Rules 1 to 3: catalog lookups are counted through an injected catalog, so
// the checks are exact and do not depend on the speed of the machine.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { createCatalog, priceOrder, priceOrders } = acme;

const skus = (n) => Array.from({ length: n }, (_, i) => `SKU-${String(i).padStart(4, '0')}`);
const items = (list) => list.map((sku, i) => ({ sku, name: sku, price: 100 + i }));

/** A catalog over items that records every call. */
function countingCatalog(list, { getMany = true } = {}) {
  const inner = createCatalog(items(list));
  const calls = { get: [], getMany: [] };
  const catalog = {
    get(sku) {
      calls.get.push(sku);
      return inner.get(sku);
    },
  };
  if (getMany) {
    catalog.getMany = (requested) => {
      calls.getMany.push([...requested]);
      return inner.getMany(requested);
    };
  }
  return { catalog, calls };
}

/** count orders, each with lines drawn round-robin from list. */
function ordersOver(list, count, linesPerOrder) {
  let next = 0;
  return Array.from({ length: count }, (_, i) => ({
    id: `O-${i}`,
    placedAt: '2026-03-12T15:00:00Z',
    lines: Array.from({ length: linesPerOrder }, () => ({ sku: list[next++ % list.length], qty: 1 + (next % 3) })),
  }));
}

const requested = (calls) => calls.getMany.flat();

describe('catalog lookups', () => {
  test('batch_looks_up_each_distinct_sku_once', () => {
    const list = skus(30);
    const { catalog, calls } = countingCatalog(list);
    const priced = priceOrders(ordersOver(list, 200, 3), { catalog });
    assert.equal(priced.length, 200);
    assert.equal(calls.get.length, 0, 'no get calls when getMany exists');
    assert.equal(calls.getMany.length, 1);
    assert.deepEqual([...requested(calls)].sort(), list);
  });

  test('getmany_batches_hold_at_most_100_skus', () => {
    const list = skus(250);
    const { catalog, calls } = countingCatalog(list);
    priceOrders(ordersOver(list, 300, 2), { catalog });
    assert.equal(calls.get.length, 0);
    assert.equal(calls.getMany.length, 3, `getMany calls: ${calls.getMany.map((c) => c.length)}`);
    for (const batch of calls.getMany) assert.ok(batch.length <= 100, `batch of ${batch.length}`);
    const all = requested(calls);
    assert.equal(new Set(all).size, all.length, 'a SKU was requested twice');
    assert.deepEqual([...all].sort(), list);
  });

  test('get_only_catalog_called_once_per_distinct_sku', () => {
    const list = skus(30);
    const { catalog, calls } = countingCatalog(list, { getMany: false });
    priceOrders(ordersOver(list, 200, 3), { catalog });
    assert.equal(calls.get.length, 30);
    assert.deepEqual([...calls.get].sort(), list);
  });

  test('single_order_looks_up_each_sku_once', () => {
    const list = skus(2);
    const order = { id: 'S-1', placedAt: '2026-03-12T15:00:00Z', lines: [list[0], list[1], list[0], list[0]].map((sku) => ({ sku, qty: 1 })) };
    const withMany = countingCatalog(list);
    assert.equal(priceOrder(order, { catalog: withMany.catalog }).subtotal, 3 * 100 + 101);
    assert.equal(withMany.calls.get.length, 0);
    assert.deepEqual(withMany.calls.getMany.map((b) => [...b].sort()), [list]);
    const getOnly = countingCatalog(list, { getMany: false });
    priceOrder(order, { catalog: getOnly.catalog });
    assert.deepEqual([...getOnly.calls.get].sort(), list);
  });

  test('lookups_not_reused_between_calls', () => {
    const prices = new Map([['TEA-01', 450], ['MUG-01', 1200]]);
    const calls = [];
    const catalog = {
      get: (sku) => (prices.has(sku) ? { sku, name: sku, price: prices.get(sku) } : undefined),
      getMany(list) {
        calls.push([...list]);
        return new Map(list.filter((s) => prices.has(s)).map((s) => [s, this.get(s)]));
      },
    };
    const orders = [{ id: 'L-1', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 2 }, { sku: 'MUG-01', qty: 1 }] }];
    assert.equal(priceOrders(orders, { catalog })[0].subtotal, 2100);
    prices.set('TEA-01', 500);
    assert.equal(priceOrders(orders, { catalog })[0].subtotal, 2200);
    prices.set('MUG-01', 1000);
    assert.equal(priceOrder(orders[0], { catalog }).subtotal, 2000);
    assert.equal(calls.length, 3, 'every call looks its SKUs up again');
  });

  test('large_batch_lookup_count', () => {
    const list = skus(400);
    const { catalog, calls } = countingCatalog(list);
    const orders = ordersOver(list, 10000, 4);
    const priced = priceOrders(orders, { catalog });
    assert.equal(priced.length, 10000);
    assert.equal(calls.get.length, 0);
    assert.equal(calls.getMany.length, 4);
    assert.equal(requested(calls).length, 400);
  });
});
