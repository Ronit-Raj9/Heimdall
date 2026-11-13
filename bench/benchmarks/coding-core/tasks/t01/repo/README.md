# ledger

Invoices with integer-cent money, discounts, tax, CSV import and JSON export.
Python 3.12, standard library only.

```
python -m ledger summary examples/invoices.csv
python -m ledger export examples/invoices.csv -o invoices.json
```

## CSV input

One row per line item, comma separated, UTF-8, with a header row:

| column | meaning |
|---|---|
| `invoice` | invoice number; rows with the same number form one invoice |
| `date` | issue date, `YYYY-MM-DD` |
| `customer` | customer name |
| `description` | line item text |
| `quantity` | decimal, at most three decimal places |
| `unit_price` | money amount such as `12.50` |
| `tax_rate` | optional, percent such as `8.875` |
| `discount` | optional, `10%` or a fixed amount such as `5.00` |

`date`, `customer`, `tax_rate` and `discount` are read from the first row of
each invoice. Later rows may leave them blank.

## Totals

Money is held as integer cents. Line amount = quantity x unit price. The
subtotal is the sum of the line amounts, the discount comes off the subtotal,
and tax is charged on what is left. Fractions of a cent are rounded half up.

## Tests

```
python -m pytest
```
