import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Inventory, OrderError } from '../src/index.js';

function setup() {
  let now = 0;
  const inv = new Inventory({ 'TEA-01': 5, 'MUG-01': 1 }, { ttlMinutes: 10, clock: () => now });
  return { inv, advance: (minutes) => { now += minutes * 60000; } };
}

test('reserve holds units until commit', () => {
  const { inv } = setup();
  inv.reserve('A', [{ sku: 'TEA-01', qty: 2 }, { sku: 'TEA-01', qty: 1 }]);
  assert.equal(inv.available('TEA-01'), 2);
  inv.commit('A');
  assert.equal(inv.onHand('TEA-01'), 2);
  assert.equal(inv.available('TEA-01'), 2);
});

test('reserve is all or nothing', () => {
  const { inv } = setup();
  assert.throws(() => inv.reserve('A', [{ sku: 'TEA-01', qty: 1 }, { sku: 'MUG-01', qty: 2 }]), /MUG-01 \(want 2, available 1\)/);
  assert.equal(inv.available('TEA-01'), 5);
});

test('reservations expire', () => {
  const { inv, advance } = setup();
  inv.reserve('A', [{ sku: 'MUG-01', qty: 1 }]);
  advance(10);
  assert.equal(inv.available('MUG-01'), 1);
  assert.throws(() => inv.commit('A'), OrderError);
});

test('release frees the units', () => {
  const { inv } = setup();
  inv.reserve('A', [{ sku: 'MUG-01', qty: 1 }]);
  assert.equal(inv.release('A'), true);
  assert.equal(inv.release('A'), false);
  assert.equal(inv.available('MUG-01'), 1);
});
