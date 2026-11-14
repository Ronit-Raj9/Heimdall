# ord02 requirements map

Every hidden test, the rule of `instruction.md` it checks, and what it asserts. Required id = `test::<test>`.

| Hidden test | Rule | Asserts |
|---|---|---|
| `invalid_promotions_refused` | 1 | buy 0, get 1.5, buy as text, get missing, sku missing, two promotions for one SKU, a non-list: `normalizeStore`, `priceOrder`, `priceOrders` throw `OrderError`, `parseStore` throws `ImportError`, message contains `promotion` |
| `valid_promotions_accepted` | 1 | a valid list (including a SKU not in the catalog) is accepted by `parseStore` and by `priceOrder` with raw settings |
| `free_units_per_complete_group` | 2, 4 | buy 2 get 1 for 1 to 7 units gives 0, 0, 1, 1, 1, 2, 2 free; buy 3 get 2 with 10 units gives 4 free; `promo` and `amount` per line |
| `units_counted_across_lines_free_from_last_line` | 2, 3 | units of one SKU on several lines count together; free units come from the last line first and spill to earlier lines |
| `order_promo_and_subtotal_after_promotions` | 4, 5 | line fields, order `promo` is the sum of line promos, subtotal is the sum of amounts after promotions |
| `coupon_minimum_and_amount_use_subtotal_after_promotions` | 5 | coupon minimum checked and percentage taken on the promoted subtotal |
| `shipping_and_tax_use_subtotal_after_promotions` | 5 | free-shipping threshold and tax on the promoted subtotal, for `priceOrder` and `priceOrders` |
| `stores_without_promotions_unchanged` | closing sentence, 4 | no promotions (or a promotion on another SKU): every `promo` is 0 and totals equal today's |
| `cli_price_shows_promotions` | 6 | `price --json` has the new fields; text output ends a promoted line with ` (promo -4.50)` and leaves other lines as today |
