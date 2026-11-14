// Rule 4: the CLI report groups orders by business day and price prints it.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { runCli } from './_workspace.mjs';

const files = {
  'catalog.json': [{ sku: 'TEA-01', name: 'Green tea', price: '4.00' }],
  'store.json': { timeZone: 'America/New_York' },
  'orders.csv': [
    'order_id,placed_at,customer,sku,qty,coupon',
    'A,2026-03-31T23:30:00-04:00,ana,TEA-01,1,',
    'B,2026-04-01T00:30:00-04:00,ben,TEA-01,2,',
    'C,2026-04-01T03:59:00Z,cy,TEA-01,3,',
    'D,2026-04-01T04:00:00Z,dee,TEA-01,4,',
    'E,2026-04-01T09:15:00+05:30,eve,TEA-01,5,',
    '',
  ].join('\n'),
};
const common = ['--catalog', 'catalog.json', '--store', 'store.json'];

describe('cli', () => {
  test('cli_report_and_price_use_store_business_day', () => {
    const report = runCli(['report', 'orders.csv', ...common, '--json'], files);
    assert.equal(report.status, 0, report.stderr);
    const rows = JSON.parse(report.stdout).map((d) => [d.day, d.orders, d.total]);
    assert.deepEqual(rows, [['2026-03-31', 3, 3600], ['2026-04-01', 2, 2400]]);

    const price = runCli(['price', 'orders.csv', ...common], files);
    assert.equal(price.status, 0, price.stderr);
    for (const [id, day] of [['A', '2026-03-31'], ['B', '2026-04-01'], ['C', '2026-03-31'], ['D', '2026-04-01'], ['E', '2026-03-31']]) {
      assert.match(price.stdout, new RegExp(`^order ${id}  ${day}  `, 'm'), `order ${id}`);
    }
  });
});
