import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatCents, parseMoney, parsePercent, percentOf } from '../src/index.js';

test('parseMoney reads dollars and cents', () => {
  assert.equal(parseMoney('12'), 1200);
  assert.equal(parseMoney('12.5'), 1250);
  assert.equal(parseMoney('0.07'), 7);
  assert.equal(parseMoney(450), 450);
  assert.throws(() => parseMoney('1.234'), RangeError);
  assert.throws(() => parseMoney('-3.00'), RangeError);
});

test('formatCents always shows two decimals', () => {
  assert.equal(formatCents(1234), '12.34');
  assert.equal(formatCents(5), '0.05');
  assert.equal(formatCents(-50), '-0.50');
});

test('percentOf is exact and rounds half up', () => {
  assert.equal(parsePercent('8.875'), 8875);
  assert.equal(percentOf(10000, '8.875'), 888);
  assert.equal(percentOf(5165, '10'), 517);
  assert.equal(percentOf(0, '25'), 0);
  assert.throws(() => parsePercent('101'), RangeError);
});
