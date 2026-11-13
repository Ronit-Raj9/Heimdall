# t01 requirements map

Every hidden test, the instruction sentence it checks, and what it asserts.

| Hidden test | Instruction sentence | Asserts |
|---|---|---|
| `test_line_amount_half_cent_rounds_up` | Rule 1 "A line amount (`LineItem.amount`) is quantity times unit price, rounded to a whole cent." and "Every rounding above is half up: a value exactly halfway between two whole cents goes to the larger one (166.5 cents becomes 167 ...)" | 0.5 x 333 = 166.5 -> 167; 1.5 x 111 = 166.5 -> 167; 0.5 x 335 = 167.5 -> 168 |
| `test_line_amount_is_exact_for_decimal_quantities` | "The calculations must be exact for the decimal values as written. For example, quantity `1.15` at a unit price of 0.50 is exactly 57.5 cents, so the line amount is 58 cents" | 1.15 x 50 -> 58; 2.675 x 180 = 481.5 -> 482; 4.35 x 50 = 217.5 -> 218 |
| `test_subtotal_is_sum_of_rounded_line_amounts` | Rule 2 "The subtotal (`Totals.subtotal`) is the sum of the rounded line amounts." with rule 1 and the half-up sentence | lines 52.5 -> 53 and 53.5 -> 54; subtotal 107 (not 106); total 107 |
| `test_tax_half_cent_rounds_up` | Rule 4 "The tax (`Totals.tax`) is the subtotal minus the discount, times the tax rate divided by 100, rounded to a whole cent." and "(... 24.5 becomes 25)" | 350 at 7 % -> tax 25, total 375 (rule 5) |
| `test_tax_is_exact_for_decimal_rates` | "tax at 1.15 % on 30.00 is exactly 34.5 cents, so the tax is 35 cents." and the exactness sentence | 3000 at 1.15 % -> 35; 2000 at 9.975 % = 199.5 -> 200; totals by rule 5 |
| `test_percentage_discount_half_cent_rounds_up_before_tax` | Rule 3 "A percentage discount is the subtotal times the percentage divided by 100, rounded to a whole cent." and rule 4 (tax on subtotal minus discount), half-up sentence | 12.5 % of 1012 = 126.5 -> 127; tax 10 % of 885 = 88.5 -> 89; total 974 |
| `test_cli_export_and_summary_follow_rounding_rules` | "The same rules apply wherever totals appear, including `python -m ledger export` (`amount_cents`, `subtotal_cents`, `discount_cents`, `tax_cents`, `total_cents`) and `python -m ledger summary`." | export JSON line amounts [142, 58], totals (200, 0, 15, 215) and (1012, 127, 0, 885); summary lines end with 2.15, 8.85, grand total 11.00 |

The last instruction sentence (public API keeps its names and arguments) is checked implicitly: every test uses `Invoice`, `LineItem`, `Discount.percent` and `Invoice.totals()` as they exist at baseline.
