# Multi-buy promotions ("buy 2, get 1 free")

Marketing wants per-product multi-buy promotions, configured in the store settings next to the coupons, for example "buy 2 green teas, get 1 free". Add them to the `@acme/orders` package by these rules:

1. Store settings accept an optional `promotions` list. Each promotion is an object `{ "sku": "TEA-01", "buy": 2, "get": 1 }`: `sku` is a non-empty string, `buy` and `get` are integers of at least 1, and a SKU has at most one promotion. A SKU that is not in the catalog is allowed. Settings that break these rules are refused wherever settings are read: `normalizeStore`, `priceOrder` and `priceOrders` throw an `OrderError`, `parseStore` throws an `ImportError`, and the error message contains the word `promotion`.
2. For each promoted SKU, all units of that SKU on an order count together, even when they are on several lines. Every complete group of `buy + get` units makes `get` of them free: free units = floor(units / (buy + get)) x get. With buy 2, get 1: 2 units give 0 free, 3 give 1, 5 give 1, 6 give 2.
3. Free units are taken from the last line of that SKU first, then from the line before it, and so on, never more than a line's quantity.
4. Every priced line has a `promo`: what its free units are worth in cents (free units x unit price), 0 when it has none. The line's `amount` becomes qty x unit price minus `promo`. The priced order has a `promo` too: the sum of its lines' `promo`.
5. The subtotal stays the sum of the line amounts, so it is taken after promotions. Everything computed from the subtotal uses that value: the coupon minimum, the coupon amount, the free-shipping threshold and the tax.
6. In the CLI, `acme-orders price --json` prints the new fields. In the text output of `acme-orders price`, a line whose `promo` is above 0 ends with ` (promo -X.XX)`, for example `  TEA-01 x3 @ 4.50 = 9.00 (promo -4.50)`. Other lines are printed as today.

Stores without promotions price exactly as before, apart from the new `promo` fields (all 0). Keep the existing public API working. The existing tests in `test/` must keep passing unchanged; you may add new test files.
