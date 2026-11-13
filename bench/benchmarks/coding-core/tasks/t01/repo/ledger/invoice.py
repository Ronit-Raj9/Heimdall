"""Invoices, line items and invoice totals."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from decimal import Decimal

from .discounts import Discount, total_discount
from .errors import LedgerError
from .money import percent_of, round_cents, to_decimal

MAX_TAX_RATE = Decimal(100)


@dataclass
class LineItem:
    """One billed line: ``quantity`` units at ``unit_price`` cents each.

    ``quantity`` may be given as a str, int or Decimal and is stored as a
    Decimal with at most three decimal places.
    """

    description: str
    quantity: Decimal
    unit_price: int

    def __post_init__(self) -> None:
        self.description = self.description.strip()
        if not self.description:
            raise LedgerError("line item needs a description")
        self.quantity = to_decimal(self.quantity, "quantity")
        price = self.unit_price
        if isinstance(price, bool) or not isinstance(price, int) or price < 0:
            raise LedgerError(f"unit price must be a non-negative int of cents, got {price!r}")

    @property
    def amount(self) -> int:
        """Line amount in cents: quantity times unit price, rounded to a whole cent."""
        return round_cents(float(self.quantity) * self.unit_price)


@dataclass(frozen=True)
class Totals:
    """Invoice totals in cents. ``total == subtotal - discount + tax``."""

    subtotal: int
    discount: int
    tax: int
    total: int


class Invoice:
    """An invoice: header fields, line items, discounts and a tax rate.

    ``tax_rate`` is a percentage (``"8.875"`` means 8.875 %). Tax is charged
    once per invoice, on the subtotal after discounts.
    """

    def __init__(
        self,
        number: str,
        issued: date,
        customer: str,
        tax_rate: str | int | Decimal = 0,
    ) -> None:
        number = number.strip()
        if not number:
            raise LedgerError("invoice number must not be empty")
        self.number = number
        self.issued = issued
        self.customer = customer.strip()
        self.tax_rate = to_decimal(tax_rate, "tax rate")
        if self.tax_rate > MAX_TAX_RATE:
            raise LedgerError(f"tax rate {self.tax_rate}% is above 100%")
        self.lines: list[LineItem] = []
        self._discounts: list[Discount] = []

    def __repr__(self) -> str:
        return (
            f"Invoice({self.number!r}, {self.issued.isoformat()}, "
            f"{self.customer!r}, lines={len(self.lines)})"
        )

    def add_line(self, description: str, quantity: str | int | Decimal, unit_price: int) -> LineItem:
        """Append a line item and return it."""
        item = LineItem(description, quantity, unit_price)  # type: ignore[arg-type]
        self.lines.append(item)
        return item

    def add_discount(self, discount: Discount) -> None:
        """Attach a discount. An invoice supports one discount."""
        if not isinstance(discount, Discount):
            raise TypeError(f"expected a Discount, got {type(discount).__name__}")
        if self._discounts:
            raise LedgerError(
                f"invoice {self.number} already has a discount ({self._discounts[0]}); "
                "only one discount per invoice is supported"
            )
        self._discounts.append(discount)

    @property
    def discounts(self) -> tuple[Discount, ...]:
        """The invoice's discounts, in the order they were added."""
        return tuple(self._discounts)

    def subtotal(self) -> int:
        """Sum of the line amounts, in cents.

        Added up from the unrounded line values so that the subtotal is
        rounded only once.
        """
        return round_cents(sum(float(item.quantity) * item.unit_price for item in self.lines))

    def totals(self) -> Totals:
        """Subtotal, discount, tax and total of this invoice, in cents."""
        subtotal = self.subtotal()
        discount = total_discount(subtotal, self._discounts)
        taxable = subtotal - discount
        tax = percent_of(taxable, self.tax_rate)
        return Totals(subtotal=subtotal, discount=discount, tax=tax, total=taxable + tax)
