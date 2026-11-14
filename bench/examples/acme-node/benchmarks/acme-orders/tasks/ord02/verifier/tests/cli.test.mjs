// Rule 6: the CLI shows promotions.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { runCli } from './_workspace.mjs';

const files = {
  'catalog.json': [
    { sku: 'TEA-01', name: 'Green tea', price: '4.50' },
    { sku: 'MUG-01', name: 'Mug', price: '12.00' },
  ],
  'store.json': { promotions: [{ sku: 'TEA-01', buy: 2, get: 1 }] },
  'orders.json': [{ id: 'W-1', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 3 }, { sku: 'MUG-01', qty: 1 }] }],
};
const common = ['--catalog', 'catalog.json', '--store', 'store.json'];

describe('cli', () => {
  test('cli_price_shows_promotions', () => {
    const json = runCli(['price', 'orders.json', ...common, '--json'], files);
    assert.equal(json.status, 0, json.stderr);
    const [p] = JSON.parse(json.stdout);
    assert.deepEqual(p.lines.map((l) => [l.promo, l.amount]), [[450, 900], [0, 1200]]);
    assert.deepEqual([p.promo, p.subtotal, p.total], [450, 2100, 2100]);

    const text = runCli(['price', 'orders.json', ...common], files);
    assert.equal(text.status, 0, text.stderr);
    assert.match(text.stdout, /^ {2}TEA-01 x3 @ 4\.50 = 9\.00 \(promo -4\.50\)$/m);
    assert.match(text.stdout, /^ {2}MUG-01 x1 @ 12\.00 = 12\.00$/m);
  });
});
