"""Money helpers."""

from decimal import ROUND_HALF_UP, Decimal


def round_money(x):
    """Round an amount to cents, halves away from zero, exactly."""
    return float(Decimal(str(x)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP))
