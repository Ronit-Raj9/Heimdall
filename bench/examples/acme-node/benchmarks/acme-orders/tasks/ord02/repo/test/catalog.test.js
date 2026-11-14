import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createCatalog, OrderError } from '../src/index.js';

test('createCatalog looks items up by SKU', () => {
  const c = createCatalog([{ sku: 'TEA-01', name: 'Green tea', price: '4.50' }, { sku: 'MUG-01', price: 1200 }]);
  assert.equal(c.size, 2);
  assert.deepEqual(c.get('TEA-01'), { sku: 'TEA-01', name: 'Green tea', price: 450 });
  assert.equal(c.get('NOPE'), undefined);
  const many = c.getMany(['MUG-01', 'NOPE']);
  assert.ok(many instanceof Map);
  assert.deepEqual([...many.keys()], ['MUG-01']);
});

test('createCatalog refuses bad items', () => {
  assert.throws(() => createCatalog([{ name: 'no sku', price: '1.00' }]), OrderError);
  assert.throws(() => createCatalog([{ sku: 'A', price: '1.00' }, { sku: 'A', price: '2.00' }]), /duplicate sku A/);
  assert.throws(() => createCatalog([{ sku: 'A', price: 'free' }]), /invalid money amount/);
});
