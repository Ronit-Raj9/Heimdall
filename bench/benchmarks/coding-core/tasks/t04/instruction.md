# Allow several discounts on one invoice

Sales wants to combine a percentage promotion with a fixed voucher on the same invoice. Today an invoice can carry only one discount. Lift that limit in both places it applies, the Python API and the CSV importer, and keep every invoice that has zero or one discount computing exactly as it does now.

## API

`Invoice.add_discount(discount)` may be called any number of times on the same invoice. `Invoice.discounts` returns the discounts in the order they were added.

## CSV

The `discount` column may hold several discounts separated by `+`, for example `10%+5.00` or `5.00+10%+2.5%`. Each part is written as a single discount is today (`12.5%` or `5.00`); spaces around a part are ignored. A part that is empty or not a valid discount, as in `10%+` or `10%+abc`, is rejected with `ledger.CsvFormatError`, whose message starts with `line N:` for the row's line number (the header row is line 1), so the CLI exits with status 1.

## How stacked discounts combine

1. All percentage discounts apply first, in the order they were added. Each one is taken from the amount left after the discounts before it (not from the original subtotal) and is rounded half up to a whole cent before the next one applies. For example, 10% and then 15% on 10.05 take 1.01 (10.05 x 10% = 1.005, rounded up) and then 1.36 (9.04 x 15% = 1.356), 2.37 in all.
2. Then all fixed discounts apply, in the order they were added.
3. The total discount never exceeds the subtotal. When a discount would take more than what is left, it takes only what is left, and every discount after it takes nothing. The discounted subtotal therefore never goes below zero.

`Totals.discount` (and `discount_cents` in `python -m ledger export`) is the sum of all the discounts taken. Tax is charged, as now, on the subtotal minus that total discount, so a fully discounted invoice has zero tax and a zero total.

In the JSON export, the `discount` field lists the invoice's discounts in the order they were added, each written as `str(discount)` writes it today (`10%`, `5.00`), joined with `+`, for example `"5.00+10%"`. It stays `null` for an invoice without a discount.

## Unchanged

An invoice with no discount or a single discount gives exactly the same `Totals` and the same export output as before, including a single fixed discount larger than the subtotal (it takes the whole subtotal) and a single percentage that lands on half a cent (rounded half up).
