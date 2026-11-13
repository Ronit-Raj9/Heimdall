# ledger: project notes

Small invoicing library with a CLI: invoices with line items, discounts and tax, CSV import, JSON export. Python 3.12, standard library only. Tests use pytest.

## Layout

- `ledger/money.py`: money helpers: `parse_amount`, `format_cents`, `to_decimal`, rounding helpers, `percent_of`.
- `ledger/invoice.py`: `LineItem`, `Invoice`, `Totals`. `Invoice.totals()` computes subtotal, discount, tax and total.
- `ledger/discounts.py`: `Discount` (percent or fixed), `parse_discounts` for the CSV `discount` column, `total_discount`.
- `ledger/dates.py`: `parse_date`, used by the CSV importer.
- `ledger/csvio.py`: CSV import, `load_invoices(path)` and `read_invoices(stream)`.
- `ledger/export.py`: JSON export, `invoice_to_dict` and `export_json`.
- `ledger/cli.py`, `ledger/__main__.py`: `python -m ledger summary FILE` and `python -m ledger export FILE [-o PATH]`.
- `tests/`: the pytest suite. `examples/invoices.csv`: sample input.

## Commands

- Run the tests from the repo root: `python -m pytest` (`pyproject.toml` points pytest at `tests/`).
- One file: `python -m pytest tests/test_invoice.py -q`.
- Try the CLI: `python -m ledger summary examples/invoices.csv` or `python -m ledger export examples/invoices.csv`.

## Conventions

- Money is always an `int` number of cents. Fractions of a cent are rounded to whole cents by the helpers in `money.py`. `format_cents` turns cents into text such as `1234.50`.
- Quantities, percentages and tax rates are `Decimal` with at most three decimal places. `to_decimal` refuses floats.
- Tax is charged once per invoice, on the subtotal after discounts.
- An invoice holds at most one discount: `Invoice.add_discount` refuses a second one, and the CSV `discount` column holds one value (`10%` or `5.00`). A discount never takes more than the subtotal.
- CSV input is comma separated, UTF-8, with a header row. One row per line item; `date`, `customer`, `tax_rate` and `discount` come from the first row of each invoice, and later rows may leave them blank or repeat the same text. JSON is the only export format.
- Dates are ISO `YYYY-MM-DD` in CSV input and in the JSON export.
- Errors: raise `LedgerError`. The importer raises `CsvFormatError`, whose message starts with `line N:` (the header is line 1).
- The CLI exits 0 on success, 1 when a file cannot be read or imported, 2 on usage errors (argparse).
- Keep the public names exported from `ledger/__init__.py` stable.
- Standard library only; do not add dependencies. Type hints and docstrings on public functions.
