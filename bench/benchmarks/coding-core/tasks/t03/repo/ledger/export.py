"""Export invoices with their totals."""

from __future__ import annotations

import json
from collections.abc import Iterable
from typing import IO, Any

from .invoice import Invoice


def invoice_to_dict(invoice: Invoice) -> dict[str, Any]:
    """A JSON-ready dict of one invoice: header fields, lines and totals in cents."""
    totals = invoice.totals()
    return {
        "invoice": invoice.number,
        "date": invoice.issued.isoformat(),
        "customer": invoice.customer,
        "tax_rate": str(invoice.tax_rate),
        "discount": "+".join(str(d) for d in invoice.discounts) or None,
        "lines": [
            {
                "description": item.description,
                "quantity": str(item.quantity),
                "unit_price_cents": item.unit_price,
                "amount_cents": item.amount,
            }
            for item in invoice.lines
        ],
        "subtotal_cents": totals.subtotal,
        "discount_cents": totals.discount,
        "tax_cents": totals.tax,
        "total_cents": totals.total,
    }


def export_json(invoices: Iterable[Invoice], fh: IO[str]) -> None:
    """Write invoices as a JSON array indented by two spaces, then a newline."""
    json.dump([invoice_to_dict(inv) for inv in invoices], fh, indent=2, ensure_ascii=False)
    fh.write("\n")
