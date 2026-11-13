"""Invoice discounts: a percentage of the subtotal or a fixed amount."""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from decimal import Decimal

from .errors import LedgerError
from .money import format_cents, parse_amount, percent_of, to_decimal

PERCENT = "percent"
FIXED = "fixed"


@dataclass(frozen=True)
class Discount:
    """A discount on an invoice subtotal.

    Build one with :meth:`percent`, :meth:`fixed` or :meth:`parse`. ``value``
    is a Decimal percentage for percent discounts and an int number of cents
    for fixed ones.
    """

    kind: str
    value: Decimal | int

    @classmethod
    def percent(cls, percent: str | int | Decimal) -> Discount:
        """A percentage discount, above 0 and at most 100 (``"12.5"`` is 12.5 %)."""
        value = to_decimal(percent, "discount percentage")
        if not 0 < value <= 100:
            raise LedgerError(f"discount percentage must be above 0 and at most 100, got {value}")
        return cls(PERCENT, value)

    @classmethod
    def fixed(cls, cents: int) -> Discount:
        """A fixed discount of ``cents`` (a positive int)."""
        if isinstance(cents, bool) or not isinstance(cents, int) or cents <= 0:
            raise LedgerError(f"fixed discount must be a positive int of cents, got {cents!r}")
        return cls(FIXED, cents)

    @classmethod
    def parse(cls, text: str) -> Discount:
        """Parse ``"10%"`` or ``"12.5%"`` (percentage) or ``"5.00"`` (fixed amount)."""
        value = text.strip()
        try:
            if value.endswith("%"):
                return cls.percent(value[:-1].strip())
            return cls.fixed(parse_amount(value))
        except ValueError:
            raise LedgerError(f"invalid discount {text!r}") from None

    def amount_on(self, base: int) -> int:
        """Cents this discount takes from ``base`` cents.

        Percentages are rounded half up to a whole cent. The result is never
        more than ``base``, so a discount cannot make an amount negative.
        """
        if base <= 0:
            return 0
        if self.kind == PERCENT:
            amount = percent_of(base, Decimal(self.value))
        else:
            amount = int(self.value)
        return min(amount, base)

    def __str__(self) -> str:
        if self.kind == PERCENT:
            return f"{self.value}%"
        return format_cents(int(self.value))


def parse_discounts(text: str) -> list[Discount]:
    """Parse the CSV ``discount`` column: empty for none, otherwise one discount."""
    value = text.strip()
    if not value:
        return []
    return [Discount.parse(value)]


def total_discount(subtotal: int, discounts: Iterable[Discount]) -> int:
    """Total discount in cents on ``subtotal`` for an invoice's discounts.

    An invoice holds at most one discount, so this is that discount's amount,
    or 0 when there is none.
    """
    items = list(discounts)
    if not items:
        return 0
    if len(items) > 1:
        raise LedgerError("only one discount per invoice is supported")
    return items[0].amount_on(subtotal)
