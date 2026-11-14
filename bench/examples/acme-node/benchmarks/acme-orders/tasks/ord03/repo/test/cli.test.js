import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const run = (...args) => spawnSync(process.execPath, ['bin/acme-orders.js', ...args], { cwd: root, encoding: 'utf8' });
const examples = ['--catalog', 'examples/catalog.json', '--store', 'examples/store.json'];

test('price prints every order', () => {
  const r = run('price', 'examples/orders.csv', ...examples);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /order A-1002 {2}2026-03-12/);
  assert.match(r.stdout, /total 55\.61/);
});

test('report groups orders by business day', () => {
  const r = run('report', 'examples/orders.csv', ...examples, '--json');
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(JSON.parse(r.stdout).map((d) => [d.day, d.orders]), [['2026-03-12', 2], ['2026-03-13', 1]]);
});

test('usage and input errors have their own exit codes', () => {
  assert.equal(run('price', 'examples/orders.csv').status, 2);
  const missing = run('price', 'nope.csv', ...examples);
  assert.equal(missing.status, 1);
  assert.match(missing.stderr, /acme-orders: /);
});
