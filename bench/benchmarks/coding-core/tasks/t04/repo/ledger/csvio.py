"""CSV import.

The file has one row per line item and a header row naming the columns.
Rows that share an ``invoice`` value belong to the same invoice; invoices
keep the order in which they first appear. The invoice-level columns
(``date``, ``customer``, ``tax_rate``, ``discount``) are read from the first
row of each invoice. Later rows of the same invoice may leave them blank or
repeat the same value; a different value is an error.

Every error raises CsvFormatError whose message starts with ``line N:``,
where N is the line number in the file (the header row is line 1).
"""

from __future__ import annotations

import csv
from collections.abc import Sequence
from pathlib import Path
from typing import IO

from .dates import parse_date
from .discounts import parse_discounts
from .errors import CsvFormatError, LedgerError
from .invoice import Invoice
from .money import parse_amount

REQUIRED_COLUMNS = ("invoice", "date", "customer", "description", "quantity", "unit_price")
OPTIONAL_COLUMNS = ("tax_rate", "discount")
INVOICE_COLUMNS = ("date", "customer", "tax_rate", "discount")


def load_invoices(path: str | Path) -> list[Invoice]:
    """Read invoices from the UTF-8 CSV file at ``path``."""
    with open(path, newline="", encoding="utf-8") as fh:
        return read_invoices(fh)


def read_invoices(fh: IO[str]) -> list[Invoice]:
    """Read invoices from an open text stream of CSV data."""
    reader = csv.DictReader(fh)
    reader.fieldnames = _check_header(reader.fieldnames)
    invoices: dict[str, Invoice] = {}
    first_rows: dict[str, dict[str, str]] = {}
    for row in reader:
        try:
            _read_row(row, invoices, first_rows)
        except CsvFormatError:
            raise
        except (LedgerError, ValueError, TypeError) as exc:
            raise CsvFormatError(f"line {reader.line_num}: {exc}") from exc
    return list(invoices.values())


def _check_header(fieldnames: Sequence[str] | None) -> list[str]:
    if not fieldnames:
        raise CsvFormatError("line 1: the file is empty")
    names = [name.strip() for name in fieldnames]
    missing = [name for name in REQUIRED_COLUMNS if name not in names]
    if missing:
        raise CsvFormatError(f"line 1: missing column(s): {', '.join(missing)}")
    return names


def _read_row(
    row: dict[str, str | None],
    invoices: dict[str, Invoice],
    first_rows: dict[str, dict[str, str]],
) -> None:
    fields = {name: (row.get(name) or "").strip() for name in REQUIRED_COLUMNS + OPTIONAL_COLUMNS}
    number = fields["invoice"]
    if not number:
        raise ValueError("missing invoice number")
    invoice = invoices.get(number)
    if invoice is None:
        invoice = _start_invoice(number, fields)
        invoices[number] = invoice
        first_rows[number] = fields
    else:
        _check_same(number, first_rows[number], fields)
    invoice.add_line(fields["description"], fields["quantity"], parse_amount(fields["unit_price"]))


def _start_invoice(number: str, fields: dict[str, str]) -> Invoice:
    if not fields["date"]:
        raise ValueError(f"invoice {number}: missing date")
    if not fields["customer"]:
        raise ValueError(f"invoice {number}: missing customer")
    invoice = Invoice(number, parse_date(fields["date"]), fields["customer"], fields["tax_rate"] or "0")
    for discount in parse_discounts(fields["discount"]):
        invoice.add_discount(discount)
    return invoice


def _check_same(number: str, first: dict[str, str], fields: dict[str, str]) -> None:
    for name in INVOICE_COLUMNS:
        value = fields[name]
        if value and value != first[name]:
            raise ValueError(f"invoice {number}: {name} {value!r} conflicts with {first[name]!r}")
