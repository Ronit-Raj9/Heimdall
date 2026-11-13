import json
from datetime import date

import pytest

from ledger import CsvFormatError, Discount, Invoice, load_invoices


def invoice_of(cents, tax_rate="0", *discounts):
    invoice = Invoice("K-1", date(2026, 3, 7), "Acme", tax_rate=tax_rate)
    invoice.add_line("Goods", "1", cents)
    for discount in discounts:
        invoice.add_discount(discount)
    return invoice


def totals_of(invoice):
    t = invoice.totals()
    return (t.subtotal, t.discount, t.tax, t.total)


def test_single_discount_and_no_discount_are_unchanged():
    assert totals_of(invoice_of(1012, "10")) == (1012, 0, 101, 1113)
    assert totals_of(invoice_of(1012, "10", Discount.percent("12.5"))) == (1012, 127, 89, 974)
    assert totals_of(invoice_of(10000, "8.25", Discount.fixed(2000))) == (10000, 2000, 660, 8660)


def test_single_fixed_discount_larger_than_subtotal_is_unchanged():
    assert totals_of(invoice_of(3000, "8", Discount.fixed(5000))) == (3000, 3000, 0, 0)


def test_add_discount_accepts_several_in_order():
    first, second, third = Discount.fixed(500), Discount.percent("10"), Discount.percent("2.5")
    invoice = invoice_of(10000, "0", first, second, third)
    assert invoice.discounts == (first, second, third)


def test_percentages_apply_before_fixed_discounts():
    invoice = invoice_of(10000, "10", Discount.fixed(1000), Discount.percent("10"))
    assert totals_of(invoice) == (10000, 2000, 800, 8800)


def test_percentages_compound_and_round_at_each_step():
    assert totals_of(invoice_of(1005, "0", Discount.percent("10"), Discount.percent("15"))) == (1005, 237, 0, 768)
    assert totals_of(invoice_of(1005, "0", Discount.percent("15"), Discount.percent("10"))) == (1005, 236, 0, 769)
    assert totals_of(invoice_of(10000, "0", Discount.percent("10"), Discount.percent("20"))) == (10000, 2800, 0, 7200)


def test_stacked_discounts_never_exceed_subtotal():
    assert totals_of(invoice_of(10000, "10", Discount.percent("50"), Discount.fixed(8000))) == (10000, 10000, 0, 0)
    assert totals_of(invoice_of(10000, "10", Discount.fixed(3000), Discount.fixed(9000))) == (10000, 10000, 0, 0)
    assert totals_of(invoice_of(10000, "10", Discount.fixed(12000), Discount.percent("10"))) == (10000, 10000, 0, 0)


def test_csv_discount_column_takes_plus_separated_discounts(write_csv, run_ledger):
    path = write_csv(
        "P-1,2026-03-07,Acme,Goods,1,100.00,10,5.00+10%\n"
        "P-2,2026-03-08,Birch,Goods,1,20.00,0, 2.5% + 1 \n"
        "P-3,2026-03-09,Cedar,Goods,1,10.12,10,12.5%\n"
        "P-4,2026-03-10,Dune,Goods,1,30.00,8,50.00\n"
    )
    result = run_ledger("export", path)
    assert result.returncode == 0, result.stderr
    rows = {
        inv["invoice"]: (inv["discount"], inv["subtotal_cents"], inv["discount_cents"], inv["tax_cents"], inv["total_cents"])
        for inv in json.loads(result.stdout.decode("utf-8"))
    }
    assert rows == {
        "P-1": ("5.00+10%", 10000, 1500, 850, 9350),
        "P-2": ("2.5%+1.00", 2000, 150, 0, 1850),
        "P-3": ("12.5%", 1012, 127, 89, 974),
        "P-4": ("50.00", 3000, 3000, 0, 0),
    }


def test_csv_rejects_an_empty_or_invalid_discount_part(write_csv, run_ledger):
    for value in ("10%+", "10%+abc"):
        path = write_csv(f"B-1,2026-03-07,Acme,Goods,1,10.00,,\nB-2,2026-03-08,Birch,Goods,1,10.00,,{value}\n")
        with pytest.raises(CsvFormatError) as info:
            load_invoices(path)
        assert str(info.value).startswith("line 3:"), value
        assert run_ledger("export", path).returncode == 1
