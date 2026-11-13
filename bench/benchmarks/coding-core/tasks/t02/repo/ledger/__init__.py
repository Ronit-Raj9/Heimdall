"""ledger: invoices with integer-cent money, discounts, tax, CSV import and export.

Public API::

    from ledger import Invoice, Discount, load_invoices

    invoice = Invoice("INV-1", date(2026, 3, 7), "Acme", tax_rate="8.875")
    invoice.add_line("Consulting", "1.5", 12000)
    invoice.add_discount(Discount.percent("10"))
    invoice.totals()   # Totals(subtotal=18000, discount=1800, tax=1438, total=17638)
"""

__version__ = "0.4.0"

from .discounts import Discount
from .errors import CsvFormatError, LedgerError
from .invoice import Invoice, LineItem, Totals
from .money import format_cents, parse_amount, percent_of, round_half_up
from .csvio import load_invoices, read_invoices
from .export import export_json, invoice_to_dict

__all__ = [
    "CsvFormatError",
    "Discount",
    "Invoice",
    "LedgerError",
    "LineItem",
    "Totals",
    "__version__",
    "export_json",
    "format_cents",
    "invoice_to_dict",
    "load_invoices",
    "parse_amount",
    "percent_of",
    "read_invoices",
    "round_half_up",
]
