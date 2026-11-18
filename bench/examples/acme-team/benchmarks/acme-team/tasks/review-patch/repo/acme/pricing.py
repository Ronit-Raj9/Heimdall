"""Basket pricing."""

from decimal import ROUND_HALF_UP, Decimal


def cents(x):
    """Round an amount to cents."""
    return float(Decimal(str(x)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP))


def basket_total(items):
    """Sum of price * quantity over (price, quantity) pairs."""
    total = 0
    for price, qty in items:
        total += price * qty
    return cents(total)
