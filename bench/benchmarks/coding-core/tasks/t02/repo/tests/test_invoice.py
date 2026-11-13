from datetime import date

import pytest

from ledger import Discount, Invoice, LedgerError, LineItem


def make_invoice(tax_rate="0"):
    return Invoice("INV-7", date(2026, 3, 7), "Acme", tax_rate=tax_rate)


def test_line_amount_whole_cents():
    assert LineItem("Hosting", "3", 1250).amount == 3750
    assert LineItem("Hours", "1.5", 2000).amount == 3000


def test_line_item_validation():
    with pytest.raises(LedgerError):
        LineItem("  ", "1", 100)
    with pytest.raises(LedgerError):
        LineItem("Thing", "1", -1)


def test_totals_without_discount():
    invoice = make_invoice(tax_rate="10")
    invoice.add_line("Design", "2", 2500)
    invoice.add_line("Hosting", "1", 1000)
    totals = invoice.totals()
    assert (totals.subtotal, totals.discount, totals.tax, totals.total) == (6000, 0, 600, 6600)


def test_percentage_discount_comes_off_before_tax():
    invoice = make_invoice(tax_rate="10")
    invoice.add_line("Design", "4", 2500)
    invoice.add_discount(Discount.percent("20"))
    totals = invoice.totals()
    assert (totals.subtotal, totals.discount, totals.tax, totals.total) == (10000, 2000, 800, 8800)


def test_fixed_discount():
    invoice = make_invoice(tax_rate="8.25")
    invoice.add_line("Design", "1", 10000)
    invoice.add_discount(Discount.fixed(2000))
    assert invoice.totals().total == 8660


def test_discount_parse_and_str():
    assert str(Discount.parse("15%")) == "15%"
    assert str(Discount.parse("7.5")) == "7.50"
    with pytest.raises(LedgerError):
        Discount.parse("ten")
