"""Stock value helpers."""

from acme.legacy import legacy_round


def stock_value(items):
    """Value of (unit_cost, count) pairs, rounded to pence."""
    return legacy_round(sum(cost * count for cost, count in items))
