// The acme-orders command line:
//   acme-orders price  ORDERS --catalog FILE [--store FILE] [--json]
//   acme-orders report ORDERS --catalog FILE [--store FILE] [--json]
// ORDERS is a .csv (point of sale) or .json (web shop) file. Exit codes: 0 on
// success, 1 when an input cannot be read, imported or priced, 2 on usage
// errors.

import { readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { createCatalog } from './catalog.js';
import { importOrdersCsv, importOrdersJson, parseStore } from './importer.js';
import { formatCents } from './money.js';
import { priceOrders } from './pricing.js';
import { ImportError, OrderError } from './errors.js';

const USAGE = 'usage: acme-orders <price|report> ORDERS --catalog FILE [--store FILE] [--json]';

/** Run the CLI with argv (without node and script). Returns the exit code. */
export function main(argv, { stdout = process.stdout, stderr = process.stderr } = {}) {
  let args;
  try {
    args = parseArgs({
      args: argv,
      allowPositionals: true,
      options: { catalog: { type: 'string' }, store: { type: 'string' }, json: { type: 'boolean', default: false } },
    });
  } catch (err) {
    stderr.write(`${err.message}\n${USAGE}\n`);
    return 2;
  }
  const [command, ordersFile, ...extra] = args.positionals;
  if (!['price', 'report'].includes(command) || !ordersFile || extra.length > 0 || !args.values.catalog) {
    stderr.write(`${USAGE}\n`);
    return 2;
  }
  try {
    const priced = priceOrders(loadOrders(ordersFile), {
      catalog: createCatalog(readJson(args.values.catalog)),
      store: args.values.store ? parseStore(readText(args.values.store)) : undefined,
    });
    const out = command === 'price' ? priceOutput(priced, args.values.json) : reportOutput(priced, args.values.json);
    stdout.write(out);
    return 0;
  } catch (err) {
    if (err instanceof ImportError || err instanceof OrderError || err.code === 'ENOENT') {
      stderr.write(`acme-orders: ${err.message}\n`);
      return 1;
    }
    throw err;
  }
}

function loadOrders(file) {
  const text = readText(file);
  if (file.endsWith('.csv')) return importOrdersCsv(text);
  if (file.endsWith('.json')) return importOrdersJson(text);
  throw new ImportError(`${file}: orders file must end in .csv or .json`);
}

function priceOutput(priced, json) {
  if (json) return `${JSON.stringify(priced, null, 2)}\n`;
  const out = [];
  for (const p of priced) {
    out.push(`order ${p.orderId}  ${p.businessDay}  ${p.customer ?? '-'}`);
    for (const l of p.lines) out.push(`  ${l.sku} x${l.qty} @ ${formatCents(l.unitPrice)} = ${formatCents(l.amount)}`);
    const coupon = p.coupon.code ? `  coupon ${p.coupon.code} (${p.coupon.status})` : '';
    out.push(`  subtotal ${formatCents(p.subtotal)}  discount ${formatCents(p.discount)}${coupon}`);
    out.push(`  shipping ${formatCents(p.shipping)}  tax ${formatCents(p.tax)}  total ${formatCents(p.total)}`);
  }
  return `${out.join('\n')}\n`;
}

function reportOutput(priced, json) {
  const days = new Map();
  for (const p of priced) {
    const d = days.get(p.businessDay) ?? { day: p.businessDay, orders: 0, total: 0 };
    d.orders += 1;
    d.total += p.total;
    days.set(p.businessDay, d);
  }
  const rows = [...days.values()].sort((a, b) => a.day.localeCompare(b.day));
  if (json) return `${JSON.stringify(rows, null, 2)}\n`;
  const grand = rows.reduce((sum, r) => sum + r.total, 0);
  const lines = rows.map((r) => `${r.day}  ${String(r.orders).padStart(3)} orders  ${formatCents(r.total).padStart(10)}`);
  return `${lines.join('\n')}\ngrand total ${formatCents(grand)}\n`;
}

function readText(file) {
  return readFileSync(file, 'utf8');
}

function readJson(file) {
  try {
    return JSON.parse(readText(file));
  } catch (err) {
    if (err instanceof SyntaxError) throw new ImportError(`${file}: invalid JSON: ${err.message}`);
    throw err;
  }
}
