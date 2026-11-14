// Rule 5: nothing inside the package uses a deprecated form.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { runCli } from './_workspace.mjs';

const files = {
  'catalog.json': [{ sku: 'TEA-01', name: 'Green tea', price: '4.50' }],
  'store.json': { timeZone: 'America/New_York', taxRate: '10' },
  'orders.csv': 'order_id,placed_at,customer,sku,qty,coupon\nA,2026-03-12T15:00:00Z,ana,TEA-01,2,\nB,2026-03-13T02:00:00Z,ben,TEA-01,1,\n',
};
const common = ['--catalog', 'catalog.json', '--store', 'store.json'];

describe('cli', () => {
  test('cli_uses_no_deprecated_api', () => {
    for (const flags of [[], ['--throw-deprecation']]) {
      const price = runCli(['price', 'orders.csv', ...common], files, flags);
      assert.equal(price.status, 0, price.stderr);
      assert.equal(price.stderr, '');
      assert.match(price.stdout, /total 9\.90$/m);
      const report = runCli(['report', 'orders.csv', ...common, '--json'], files, flags);
      assert.equal(report.status, 0, report.stderr);
      assert.equal(report.stderr, '');
      assert.deepEqual(JSON.parse(report.stdout).map((d) => [d.day, d.orders, d.total]), [['2026-03-12', 2, 1485]]);
    }
  });
});
