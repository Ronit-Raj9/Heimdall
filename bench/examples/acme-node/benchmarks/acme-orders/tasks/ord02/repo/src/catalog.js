// The catalog maps a SKU to its item: { sku, name, price } with price in
// cents. Pricing only needs an object with get(sku); production adapters wrap
// the pricing database, where every call is a round trip, and also offer
// getMany(skus). createCatalog is the in-memory adapter used by the CLI and
// the tests.

import { parseMoney } from './money.js';
import { OrderError } from './errors.js';

/**
 * Build an in-memory catalog from items such as
 * { sku: 'TEA-01', name: 'Green tea', price: '4.50' }. A price may be text
 * ("4.50") or integer cents.
 */
export function createCatalog(items) {
  if (!Array.isArray(items)) throw new OrderError('catalog: items must be an array');
  const bySku = new Map();
  for (const [i, raw] of items.entries()) {
    const where = `catalog item ${i + 1}`;
    if (typeof raw?.sku !== 'string' || raw.sku.trim() === '') throw new OrderError(`${where}: sku is required`);
    if (bySku.has(raw.sku)) throw new OrderError(`${where}: duplicate sku ${raw.sku}`);
    let price;
    try {
      price = parseMoney(raw.price);
    } catch (err) {
      throw new OrderError(`${where}: ${err.message}`);
    }
    bySku.set(raw.sku, Object.freeze({ sku: raw.sku, name: String(raw.name ?? raw.sku), price }));
  }
  return {
    /** The item for sku, or undefined. */
    get(sku) {
      return bySku.get(sku);
    },
    /** A Map from each known SKU in skus to its item; unknown SKUs are absent. */
    getMany(skus) {
      const found = new Map();
      for (const sku of skus) {
        const item = bySku.get(sku);
        if (item) found.set(sku, item);
      }
      return found;
    },
    get size() {
      return bySku.size;
    },
  };
}
