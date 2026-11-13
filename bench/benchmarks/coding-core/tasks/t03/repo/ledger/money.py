"""Money and decimal helpers.

Every amount of money in ledger is an ``int`` number of cents. Quantities,
percentages and tax rates are :class:`decimal.Decimal` values with at most
three decimal places. Arithmetic that can produce a fraction of a cent goes
through :func:`round_half_up`, so results never depend on float
representation.
"""

from __future__ import annotations

import re
from decimal import ROUND_HALF_UP, Decimal

MAX_PLACES = 3

_AMOUNT_RE = re.compile(r"([0-9]+)(?:\.([0-9]{1,2}))?")
_DECIMAL_RE = re.compile(r"[0-9]+(?:\.[0-9]{1,%d})?" % MAX_PLACES)


def parse_amount(text: str) -> int:
    """Parse a non-negative money amount such as ``"12.5"`` into cents (1250).

    At most two decimal places are allowed and surrounding whitespace is
    ignored. Raises ValueError for anything else, including an empty string.
    """
    value = text.strip()
    match = _AMOUNT_RE.fullmatch(value)
    if match is None:
        raise ValueError(f"invalid amount {text!r}")
    whole, frac = match.group(1), match.group(2) or ""
    return int(whole) * 100 + int(frac.ljust(2, "0"))


def format_cents(cents: int) -> str:
    """Format cents as a plain decimal string: 123456 -> ``"1234.56"``.

    Negative amounts get a leading minus sign (-5 -> ``"-0.05"``). There is
    no currency symbol and no thousands separator.
    """
    sign = "-" if cents < 0 else ""
    whole, frac = divmod(abs(cents), 100)
    return f"{sign}{whole}.{frac:02d}"


def to_decimal(value: str | int | Decimal, what: str) -> Decimal:
    """Convert a quantity, percentage or rate to a non-negative Decimal.

    Accepts ints, Decimals and strings such as ``"1.5"``. Floats are refused
    because most decimal fractions have no exact float value. ``what`` names
    the value in error messages.
    """
    if isinstance(value, bool) or not isinstance(value, (str, int, Decimal)):
        raise TypeError(f"{what} must be a str, int or Decimal, not {type(value).__name__}")
    if isinstance(value, str):
        text = value.strip()
        if _DECIMAL_RE.fullmatch(text) is None:
            raise ValueError(f"invalid {what} {value!r}")
        result = Decimal(text)
    else:
        result = Decimal(value)
    if not result.is_finite() or result < 0:
        raise ValueError(f"invalid {what} {value!r}")
    if int(result.as_tuple().exponent) < -MAX_PLACES:
        raise ValueError(f"{what} {value!r} has more than {MAX_PLACES} decimal places")
    return result


def round_half_up(value: Decimal) -> int:
    """Round a Decimal number of cents to a whole cent.

    A value exactly halfway between two whole cents goes to the one further
    from zero: 166.5 -> 167.
    """
    return int(value.to_integral_value(rounding=ROUND_HALF_UP))


def percent_of(cents: int, percent: Decimal) -> int:
    """Return ``percent`` % of ``cents``, rounded half up to a whole cent."""
    return round_half_up(Decimal(cents) * percent / 100)
