# ord04 requirements map

Every hidden test, the rule of `instruction.md` it checks, and what it asserts. Required id = `test::<test>`. Lookups are counted through an injected catalog, so every check is exact and independent of machine speed.

| Hidden test | Rule | Asserts |
|---|---|---|
| `batch_looks_up_each_distinct_sku_once` | 1, 2 | 200 orders x 3 lines over 30 SKUs: one `getMany` call holding the 30 SKUs, no `get` |
| `getmany_batches_hold_at_most_100_skus` | 2 | 250 distinct SKUs: 3 `getMany` calls of at most 100, no SKU twice, all 250 covered |
| `get_only_catalog_called_once_per_distinct_sku` | 2 | a catalog without `getMany` gets one `get` per distinct SKU |
| `single_order_looks_up_each_sku_once` | 1, 2 | one order naming a SKU three times: one lookup per distinct SKU, through `getMany` or `get` |
| `lookups_not_reused_between_calls` | 3 | prices changed between calls are seen by the next `priceOrders` and `priceOrder` call; every call makes its own lookup |
| `large_batch_lookup_count` | 1, 2 | 10,000 orders x 4 lines over 400 SKUs: exactly 4 `getMany` calls, 400 SKUs requested, no `get` |
| `results_match_one_by_one_pricing` | 4 | 60 varied orders (coupons, time zone, tax, shipping): batch equals one-by-one pricing, in input order, and one order's totals match fixed values |
| `errors_match_one_by_one_pricing` | 4 | the first failing order in input order decides the error (`order B-2: unknown sku NOPE` or `order C-3: quantity ...`), same for `priceOrder`; an empty batch prices to `[]` |
