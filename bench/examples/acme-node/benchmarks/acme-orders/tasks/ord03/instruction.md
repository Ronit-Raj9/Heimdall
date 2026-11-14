# Move pricing to an options object and rename formatMoney, without breaking callers

The positional signature `priceOrder(order, catalog, store)` cannot grow: currencies and promotions are next, and every new argument would be one more position. `formatMoney` is also misnamed, since it formats cents. Five internal services call these functions, so this minor release must keep the old forms working while steering callers to the new ones.

Change the `@acme/orders` package as follows:

1. `priceOrder(order, options)` and `priceOrders(orders, options)` take an options object `{ catalog, store }`. `store` is optional, with the same defaults as today. When `options.catalog` is missing or has no `get` method, they throw a `TypeError`.
2. The positional forms `priceOrder(order, catalog[, store])` and `priceOrders(orders, catalog[, store])` keep working, with or without the store argument. For the same inputs they return exactly what the options form returns, and throw the same errors.
3. `formatCents(cents)` is exported and behaves as `formatMoney` does today. `formatMoney` keeps working and gives identical output.
4. Each use of a deprecated form (a positional `priceOrder` or `priceOrders` call, a `formatMoney` call) emits a Node.js `DeprecationWarning` through `process.emitWarning`, so that `--no-deprecation` silences it and `--throw-deprecation` makes it fatal, as for Node's own deprecations:
   - positional `priceOrder` and `priceOrders`: code `ACME_DEP001`, with a message that contains `{ catalog, store }`;
   - `formatMoney`: code `ACME_DEP002`, with a message that contains `formatCents`.

   Each code is emitted at most once per process, however many deprecated calls are made; the two pricing functions share `ACME_DEP001`. The two codes are independent of each other.
5. Nothing inside the package uses a deprecated form. The CLI (`acme-orders price` and `acme-orders report`) and every other exported function run without emitting any warning.

Everything else in the public API (`src/index.js`) stays as it is. The existing tests in `test/` exercise the old forms and must keep passing unchanged; you may add new test files.
