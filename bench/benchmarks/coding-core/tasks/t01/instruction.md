# Fix rounding in invoice totals

Customers report invoices that are off by a cent. Some totals come out one cent low when an amount lands exactly on half a cent, and some invoices show a subtotal that is not the sum of the line amounts printed above it.

Make the `ledger` package compute invoice totals by these rules. Money is an integer number of cents throughout. Quantities, discount percentages and tax rates are decimal values with at most three decimal places (for example `"1.15"` or `"9.975"`).

1. A line amount (`LineItem.amount`) is quantity times unit price, rounded to a whole cent.
2. The subtotal (`Totals.subtotal`) is the sum of the rounded line amounts.
3. A percentage discount is the subtotal times the percentage divided by 100, rounded to a whole cent. A fixed discount is taken as it is, but never more than the subtotal.
4. The tax (`Totals.tax`) is the subtotal minus the discount, times the tax rate divided by 100, rounded to a whole cent. It is computed once per invoice.
5. The total (`Totals.total`) is subtotal minus discount plus tax.

Every rounding above is half up: a value exactly halfway between two whole cents goes to the larger one (166.5 cents becomes 167, 24.5 becomes 25), and any other value goes to the nearest whole cent.

The calculations must be exact for the decimal values as written. For example, quantity `1.15` at a unit price of 0.50 is exactly 57.5 cents, so the line amount is 58 cents; tax at 1.15 % on 30.00 is exactly 34.5 cents, so the tax is 35 cents.

The same rules apply wherever totals appear, including `python -m ledger export` (`amount_cents`, `subtotal_cents`, `discount_cents`, `tax_cents`, `total_cents`) and `python -m ledger summary`.

Keep the public API as it is: `Invoice`, `LineItem`, `Discount` and `Invoice.totals()` keep their names and arguments.
