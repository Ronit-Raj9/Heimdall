from decimal import Decimal

import pytest

from ledger import format_cents, parse_amount
from ledger.money import to_decimal


@pytest.mark.parametrize(
    "text, cents",
    [("0", 0), ("12", 1200), ("12.5", 1250), ("12.34", 1234), (" 7.05 ", 705), ("1000.00", 100000)],
)
def test_parse_amount(text, cents):
    assert parse_amount(text) == cents


@pytest.mark.parametrize("text", ["", "abc", "1.234", "-5", "1,50", "$3"])
def test_parse_amount_rejects_bad_input(text):
    with pytest.raises(ValueError):
        parse_amount(text)


@pytest.mark.parametrize("cents, text", [(0, "0.00"), (5, "0.05"), (1250, "12.50"), (123456, "1234.56"), (-5, "-0.05")])
def test_format_cents(cents, text):
    assert format_cents(cents) == text


def test_to_decimal_accepts_str_int_and_decimal():
    assert to_decimal("1.5", "quantity") == Decimal("1.5")
    assert to_decimal(3, "quantity") == Decimal(3)
    assert to_decimal(Decimal("0.125"), "quantity") == Decimal("0.125")


def test_to_decimal_rejects_floats_and_extra_places():
    with pytest.raises(TypeError):
        to_decimal(1.5, "quantity")
    with pytest.raises(ValueError):
        to_decimal("1.0005", "quantity")
