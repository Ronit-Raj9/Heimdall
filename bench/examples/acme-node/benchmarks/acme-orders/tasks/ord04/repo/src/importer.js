// Order and settings import. Orders come from the web shop as JSON and from
// the point-of-sale export as CSV with one row per line item:
//   order_id,placed_at,customer,sku,qty,coupon
// Rows of one order share order_id; placed_at, customer and coupon come from
// its first row and later rows may leave them blank or repeat them. Both
// importers return orders as pricing takes them, with placedAt as a Date.

import { parseTimestamp } from './dates.js';
import { normalizeStore } from './store.js';
import { ImportError, OrderError } from './errors.js';

const CSV_COLUMNS = ['order_id', 'placed_at', 'customer', 'sku', 'qty', 'coupon'];
const SHARED_COLUMNS = ['placed_at', 'customer', 'coupon'];

/**
 * Split CSV text into rows of fields: comma separated, fields may be quoted
 * with "" for a literal quote, LF or CRLF line ends. Blank lines are skipped.
 * Each row records its 1-based line number as row.line.
 */
export function parseCsv(text) {
  const rows = [];
  let row = [];
  let field = '';
  let quoted = false;
  let line = 1;
  let rowLine = 1;
  const endRow = () => {
    row.push(field);
    if (row.length > 1 || row[0] !== '') rows.push(Object.assign(row, { line: rowLine }));
    row = [];
    field = '';
  };
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"' && text[i + 1] === '"') { field += '"'; i++; }
      else if (ch === '"') quoted = false;
      else { if (ch === '\n') line++; field += ch; }
    } else if (ch === '"' && field === '') quoted = true;
    else if (ch === ',') { row.push(field); field = ''; }
    else if (ch === '\n') { endRow(); line++; rowLine = line; }
    else if (ch !== '\r') field += ch;
  }
  if (quoted) throw new ImportError(`line ${rowLine}: unterminated quoted field`);
  if (field !== '' || row.length > 0) endRow();
  return rows;
}

/** Import point-of-sale CSV text (see the header comment) into orders. */
export function importOrdersCsv(text) {
  const [header, ...rows] = parseCsv(text.replace(/^﻿/, ''));
  if (!header || header.join(',') !== CSV_COLUMNS.join(',')) {
    throw new ImportError(`line 1: header must be ${CSV_COLUMNS.join(',')}`);
  }
  const orders = new Map();
  for (const row of rows) {
    if (row.length !== CSV_COLUMNS.length) throw new ImportError(`line ${row.line}: expected ${CSV_COLUMNS.length} fields, got ${row.length}`);
    const rec = Object.fromEntries(CSV_COLUMNS.map((c, i) => [c, row[i].trim()]));
    if (rec.order_id === '') throw new ImportError(`line ${row.line}: order_id is empty`);
    let order = orders.get(rec.order_id);
    if (!order) {
      order = { id: rec.order_id, placedAt: timestamp(rec.placed_at, `line ${row.line}`), customer: rec.customer || null, coupon: rec.coupon || null, lines: [], first: rec };
      orders.set(rec.order_id, order);
    } else {
      for (const c of SHARED_COLUMNS) {
        if (rec[c] !== '' && rec[c] !== order.first[c]) throw new ImportError(`line ${row.line}: ${c} differs from the first row of order ${rec.order_id}`);
      }
    }
    order.lines.push({ sku: requireText(rec.sku, `line ${row.line}: sku`), qty: quantity(rec.qty, `line ${row.line}`) });
  }
  return [...orders.values()].map(({ first, ...order }) => order);
}

/** Import web-shop JSON: an array of orders, or { "orders": [...] }. */
export function importOrdersJson(text) {
  let data;
  try {
    data = JSON.parse(text);
  } catch (err) {
    throw new ImportError(`invalid JSON: ${err.message}`);
  }
  const list = Array.isArray(data) ? data : data?.orders;
  if (!Array.isArray(list)) throw new ImportError('expected an array of orders or {"orders": [...]}');
  return list.map((raw, i) => {
    const where = `order ${i + 1}`;
    if (!Array.isArray(raw?.lines) || raw.lines.length === 0) throw new ImportError(`${where}: lines must be a non-empty array`);
    return {
      id: requireText(raw.id, `${where}: id`),
      placedAt: timestamp(raw.placedAt, where),
      customer: raw.customer ?? null,
      coupon: raw.coupon ?? null,
      lines: raw.lines.map((l) => ({ sku: requireText(l?.sku, `${where}: sku`), qty: quantity(l?.qty, where) })),
    };
  });
}

/** Parse store settings JSON (store.json) into normalized settings. */
export function parseStore(text) {
  let data;
  try {
    data = JSON.parse(text);
  } catch (err) {
    throw new ImportError(`store settings: invalid JSON: ${err.message}`);
  }
  try {
    return normalizeStore(data);
  } catch (err) {
    if (err instanceof OrderError) throw new ImportError(err.message);
    throw err;
  }
}

function timestamp(value, where) {
  try {
    return parseTimestamp(value);
  } catch (err) {
    throw new ImportError(`${where}: ${err.message}`);
  }
}

function quantity(value, where) {
  const text = String(value ?? '').trim();
  if (!/^\d+$/.test(text) || Number(text) < 1) throw new ImportError(`${where}: qty must be a positive integer, got "${value}"`);
  return Number(text);
}

function requireText(value, what) {
  if (typeof value !== 'string' || value.trim() === '') throw new ImportError(`${what} is required`);
  return value.trim();
}
