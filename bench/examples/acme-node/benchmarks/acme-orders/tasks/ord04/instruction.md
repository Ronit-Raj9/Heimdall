# Batch pricing makes one catalog round trip per order line

The nightly job prices about 40,000 point-of-sale orders with `priceOrders` and takes 25 minutes. Almost all of that time is spent in the catalog: in production the catalog adapter wraps the pricing database, every call is a round trip, and `priceOrders` calls `catalog.get(sku)` once for every line of every order, even when thousands of lines name the same SKU.

The production adapter, like `createCatalog`, also has `getMany(skus)`: it returns a `Map` from each SKU it knows to its item, and SKUs it does not know are absent from the `Map`. The database accepts at most 100 SKUs per `getMany` call.

Make the `@acme/orders` package follow these rules:

1. One `priceOrders(orders, options)` call looks up each distinct SKU at most once, however many orders and lines name it. One `priceOrder(order, options)` call does the same for its own lines.
2. When the catalog has a `getMany` method, all lookups go through `getMany` (no `get` calls), with at most 100 SKUs per call, so a call that needs N distinct SKUs makes ceil(N / 100) `getMany` calls. A catalog without `getMany` gets one `get` call per distinct SKU.
3. Lookups are not reused between calls: prices change during the day, so every `priceOrders` or `priceOrder` call looks its SKUs up again and uses the items the catalog returns at that time.
4. Results are exactly what pricing the orders one at a time gives today, in input order. Errors too: the error thrown is the one that pricing the orders one at a time, in input order, would throw first, with the same class and message (for example `order B-2: unknown sku NOPE`).

Keep the public API (`src/index.js`) as it is; the CLI keeps working. The existing tests in `test/` must keep passing unchanged; you may add new test files.
