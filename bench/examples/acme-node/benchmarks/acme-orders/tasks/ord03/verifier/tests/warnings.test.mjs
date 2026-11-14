// Rule 4: deprecated forms warn through Node's warning system, once per
// process and code. Every case runs in a new Node process.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { runSnippet } from './_workspace.mjs';

const SETUP = `
const warnings = [];
process.on('warning', (w) => warnings.push({ name: w.name, code: w.code, message: w.message }));
const catalog = acme.createCatalog([{ sku: 'TEA-01', name: 'Green tea', price: '4.50' }]);
const store = { timeZone: 'UTC', taxRate: '10' };
const order = { id: 'D-1', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 2 }] };
const report = (extra = {}) => setImmediate(() => console.log(JSON.stringify({ warnings, ...extra })));
`;

function run(body, flags) {
  const r = runSnippet(SETUP + body, flags);
  assert.equal(r.status, 0, r.stderr);
  return { ...JSON.parse(r.stdout.trim().split('\n').pop()), stderr: r.stderr };
}

describe('deprecation warnings', () => {
  test('positional_forms_warn_once_with_code', () => {
    const out = run(`
const totals = [];
for (let i = 0; i < 3; i++) totals.push(acme.priceOrder(order, catalog, store).total);
totals.push(acme.priceOrder(order, catalog).total);
for (let i = 0; i < 2; i++) totals.push(acme.priceOrders([order], catalog, store)[0].total);
report({ totals });
`);
    assert.deepEqual(out.totals, [990, 990, 990, 900, 990, 990]);
    assert.equal(out.warnings.length, 1, JSON.stringify(out.warnings));
    assert.equal(out.warnings[0].name, 'DeprecationWarning');
    assert.equal(out.warnings[0].code, 'ACME_DEP001');
    assert.ok(out.warnings[0].message.includes('{ catalog, store }'), out.warnings[0].message);
  });

  test('priceorders_positional_warns_first', () => {
    const out = run(`
acme.priceOrders([order], catalog);
acme.priceOrder(order, catalog, store);
report();
`);
    assert.deepEqual(out.warnings.map((w) => [w.name, w.code]), [['DeprecationWarning', 'ACME_DEP001']]);
  });

  test('format_money_warns_once_independently', () => {
    const out = run(`
const texts = [acme.formatMoney(1234), acme.formatMoney(5), acme.formatMoney(-50)];
acme.priceOrder(order, catalog, store);
acme.formatMoney(1);
acme.priceOrders([order], catalog);
report({ texts });
`);
    assert.deepEqual(out.texts, ['12.34', '0.05', '-0.50']);
    const byCode = Object.fromEntries(out.warnings.map((w) => [w.code, w]));
    assert.equal(out.warnings.length, 2, JSON.stringify(out.warnings));
    assert.equal(byCode.ACME_DEP002?.name, 'DeprecationWarning');
    assert.ok(byCode.ACME_DEP002.message.includes('formatCents'), byCode.ACME_DEP002.message);
    assert.equal(byCode.ACME_DEP001?.name, 'DeprecationWarning');
  });

  test('new_forms_do_not_warn', () => {
    const out = run(`
acme.priceOrder(order, { catalog, store });
acme.priceOrder(order, { catalog });
acme.priceOrders([order, order], { catalog, store });
acme.formatCents(1234);
acme.importOrdersJson(JSON.stringify([{ id: 'J-1', placedAt: '2026-03-12T15:00:00Z', lines: [{ sku: 'TEA-01', qty: 1 }] }]));
acme.parseStore(JSON.stringify(store));
report();
`);
    assert.deepEqual(out.warnings, []);
    assert.equal(out.stderr, '');
  });

  test('no_deprecation_flag_silences_warnings', () => {
    const out = run(`
const totals = [acme.priceOrder(order, catalog, store).total, acme.priceOrders([order], catalog)[0].total];
report({ totals, text: acme.formatMoney(250) });
`, ['--no-deprecation']);
    assert.deepEqual(out.totals, [990, 900]);
    assert.equal(out.text, '2.50');
    assert.deepEqual(out.warnings, []);
    assert.equal(out.stderr, '');
  });

  test('throw_deprecation_flag_makes_warnings_fatal', () => {
    const deprecated = [
      ['acme.priceOrder(order, catalog, store);', 'ACME_DEP001'],
      ['acme.priceOrders([order], catalog);', 'ACME_DEP001'],
      ['acme.formatMoney(1);', 'ACME_DEP002'],
    ];
    for (const [call, code] of deprecated) {
      const r = runSnippet(SETUP + call, ['--throw-deprecation']);
      assert.notEqual(r.status, 0, `${call} should be fatal`);
      assert.match(r.stderr, /DeprecationWarning/, call);
      assert.ok(r.stderr.includes(code), `${call}: ${r.stderr}`);
    }
    const ok = runSnippet(SETUP + 'acme.priceOrder(order, { catalog, store }); acme.formatCents(1); report();', ['--throw-deprecation']);
    assert.equal(ok.status, 0, ok.stderr);
  });
});
