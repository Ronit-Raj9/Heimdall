"""Deprecated helpers kept for old callers. Do not use in new code."""


def legacy_round(x):
    """Round to cents the old way (float arithmetic, wrong for some halves)."""
    return float(int(x * 100 + 0.5)) / 100
