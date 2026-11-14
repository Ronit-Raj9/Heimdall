# @acme/orders: project notes

Order pricing, coupons, stock reservations and order import for Acme's tea stores, with the `acme-orders` CLI used by the back office. Node.js 22, ES modules, no npm dependencies. Tests use Node's built-in runner (`node:test`).

## Layout

- `src/money.js`: cents helpers: `parseMoney`, `formatCents`, `parsePercent`, `percentOf`.
- `src/dates.js`: `parseTimestamp` (ISO 8601 with `Z` or `+HH:MM`), `businessDay`, `isIsoDate`.
- `src/catalog.js`: `createCatalog`, the in-memory catalog adapter: `get(sku)` returns the item or undefined, `getMany(skus)` returns a `Map` of the known SKUs.
- `src/store.js`: `normalizeStore`: checks store settings (time zone, tax rate, shipping, coupons) and fills defaults.
- `src/discounts.js`: coupons: `findCoupon`, `couponDiscount`, `applyCoupon`.
- `src/pricing.js`: `priceOrder(order, { catalog, store })`, `priceOrders(orders, { catalog, store })`.
- `src/inventory.js`: `Inventory`: `reserve`, `release`, `commit`; reservations expire after `ttlMinutes`.
- `src/importer.js`: `parseCsv`, `importOrdersCsv`, `importOrdersJson`, `parseStore`.
- `src/cli.js`, `bin/acme-orders.js`: `acme-orders price|report ORDERS --catalog FILE [--store FILE] [--json]`.
- `src/index.js`: the public API. `test/`: the test suite. `examples/`: sample catalog, store settings and orders.

## Commands

- All tests: `npm test` (runs `node --test`, which finds `test/*.test.js`).
- One file: `node --test test/pricing.test.js`.
- Try the CLI: `node bin/acme-orders.js price examples/orders.csv --catalog examples/catalog.json --store examples/store.json`.

## Conventions

- Money is an integer number of cents everywhere; text amounts ("4.50") are converted at the edges. Percentages are applied exactly and rounded half up (`percentOf`).
- Line amount is qty times unit price; the subtotal is the sum of line amounts. Tax is `taxRate` % of (subtotal minus coupon discount); shipping is not taxed. Shipping is free when subtotal minus discount reaches `shipping.freeOver`.
- Business days are UTC calendar dates: `businessDay` ignores the store time zone, which is only used for display. Coupon windows (`validFrom`, `validTo`) are UTC dates, both ends included.
- Errors: `OrderError` for pricing, stock and settings problems; `ImportError` for input files. CSV import errors start with `line N:` (the header is line 1).
- CLI exit codes: 0 on success, 1 when an input cannot be read, imported or priced, 2 on usage errors.
- Keep the public exports in `src/index.js` stable. Do not add dependencies.
