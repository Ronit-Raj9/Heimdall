# Add CSV output to the export command

Accounting wants to load invoice totals into a spreadsheet. Add a `--format` option to `python -m ledger export FILE` so it can write CSV as well as JSON.

## The option

- `--format json` is the default. With it, or with no `--format` at all, the output stays exactly what `export` writes today, byte for byte.
- `--format csv` writes the CSV described below instead.
- Any other value is rejected as a usage error: exit status 2 and nothing written to standard output.
- `-o PATH` / `--output PATH` works with both formats: the file receives exactly the bytes that would otherwise go to standard output, and nothing is written to standard output.

## The CSV

- One header row, then one row per invoice, in the same order as the JSON export.
- The header row is exactly `invoice,date,customer,lines,subtotal,discount,tax,total`, and the columns appear in that order.
- `invoice` and `customer` are the invoice number and customer name. `date` is the issue date as `YYYY-MM-DD`. `lines` is the number of line items on the invoice.
- `subtotal`, `discount`, `tax` and `total` are the invoice totals written as decimal amounts with exactly two digits after the point, no currency symbol and no thousands separator: 2160.00, 0.00, 1524.25. An invoice without a discount has `0.00` in `discount`.
- Fields are separated by a comma. A field is enclosed in double quotes only when it contains a comma, a double quote or a line break; a double quote inside a quoted field is written twice. No other field is quoted.
- Every row, including the header and the last row, ends with a single line feed (`\n`). There are no carriage returns and no blank lines.
- When the input file has a header but no rows, the output is just the header row.

For example, for `examples/invoices.csv` the output of `python -m ledger export examples/invoices.csv --format csv` is:

```
invoice,date,customer,lines,subtotal,discount,tax,total
INV-1001,2026-03-02,Northwind Traders,2,1400.00,0.00,124.25,1524.25
INV-1002,2026-03-05,"Hale, Brook & Sons",1,2400.00,240.00,0.00,2160.00
INV-1003,2026-03-09,Juniper Labs,2,690.00,50.00,46.40,686.40
```
