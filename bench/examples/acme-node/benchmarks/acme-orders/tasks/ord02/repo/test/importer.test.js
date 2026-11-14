import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ImportError, importOrdersCsv, importOrdersJson, parseCsv, parseStore } from '../src/index.js';

const HEADER = 'order_id,placed_at,customer,sku,qty,coupon\n';

test('parseCsv handles quotes and CRLF', () => {
  const rows = parseCsv('a,"b,c","say ""hi"""\r\n\r\nd,e,f\n');
  assert.deepEqual(rows.map((r) => [...r]), [['a', 'b,c', 'say "hi"'], ['d', 'e', 'f']]);
  assert.deepEqual(rows.map((r) => r.line), [1, 3]);
});

test('CSV rows are grouped into orders', () => {
  const orders = importOrdersCsv(`${HEADER}A,2026-03-12T14:05:00Z,ana,TEA-01,2,\nB,2026-03-12T15:00:00Z,ben,MUG-01,1,SPRING10\nA,,,MUG-01,1,\n`);
  assert.equal(orders.length, 2);
  assert.deepEqual(orders[0].lines, [{ sku: 'TEA-01', qty: 2 }, { sku: 'MUG-01', qty: 1 }]);
  assert.equal(orders[0].placedAt.toISOString(), '2026-03-12T14:05:00.000Z');
  assert.equal(orders[1].coupon, 'SPRING10');
});

test('CSV errors name the line', () => {
  assert.throws(() => importOrdersCsv(`${HEADER}A,2026-03-12T14:05:00Z,ana,TEA-01,two,\n`), /^ImportError: line 2: qty/);
  assert.throws(() => importOrdersCsv(`${HEADER}A,2026-03-12T14:05:00Z,ana,TEA-01,1,\nA,2026-03-13T14:05:00Z,,MUG-01,1,\n`), /line 3: placed_at differs/);
  assert.throws(() => importOrdersCsv('id,sku\n'), ImportError);
});

test('JSON orders are checked', () => {
  const [o] = importOrdersJson('[{"id":"W-1","placedAt":"2026-03-14T18:20:00Z","lines":[{"sku":"TEA-01","qty":1}]}]');
  assert.equal(o.placedAt.toISOString(), '2026-03-14T18:20:00.000Z');
  assert.throws(() => importOrdersJson('{"orders":[{"id":"W-1","placedAt":"yesterday","lines":[{"sku":"TEA-01","qty":1}]}]}'), /order 1: invalid timestamp/);
});

test('store settings are validated', () => {
  assert.equal(parseStore('{"timeZone":"Asia/Kolkata"}').timeZone, 'Asia/Kolkata');
  assert.throws(() => parseStore('{"timezone":"UTC"}'), /unknown setting "timezone"/);
  assert.throws(() => parseStore('{"coupons":[{"code":"X"}]}'), ImportError);
});
