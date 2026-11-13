import json
from datetime import date

from ledger import Discount, Invoice, LineItem


def totals_of(invoice):
    t = invoice.totals()
    return (t.subtotal, t.discount, t.tax, t.total)


def test_line_amount_half_cent_rounds_up():
    assert LineItem("Parts", "0.5", 333).amount == 167
    assert LineItem("Labour", "1.5", 111).amount == 167
    assert LineItem("Bolts", "0.5", 335).amount == 168


def test_line_amount_is_exact_for_decimal_quantities():
    assert LineItem("Clips", "1.15", 50).amount == 58
    assert LineItem("Hours", "2.675", 180).amount == 482
    assert LineItem("Rope", "4.35", 50).amount == 218


def test_subtotal_is_sum_of_rounded_line_amounts():
    invoice = Invoice("R-3", date(2026, 3, 7), "Acme")
    invoice.add_line("Pens", "0.5", 105)
    invoice.add_line("Pads", "0.5", 107)
    assert [item.amount for item in invoice.lines] == [53, 54]
    assert totals_of(invoice) == (107, 0, 0, 107)


def test_tax_half_cent_rounds_up():
    invoice = Invoice("R-4", date(2026, 3, 7), "Acme", tax_rate="7")
    invoice.add_line("Service", "1", 350)
    assert totals_of(invoice) == (350, 0, 25, 375)


def test_tax_is_exact_for_decimal_rates():
    first = Invoice("R-5", date(2026, 3, 7), "Acme", tax_rate="1.15")
    first.add_line("Service", "1", 3000)
    assert totals_of(first) == (3000, 0, 35, 3035)
    second = Invoice("R-6", date(2026, 3, 7), "Acme", tax_rate="9.975")
    second.add_line("Service", "1", 2000)
    assert totals_of(second) == (2000, 0, 200, 2200)


def test_percentage_discount_half_cent_rounds_up_before_tax():
    invoice = Invoice("R-7", date(2026, 3, 7), "Acme", tax_rate="10")
    invoice.add_line("Kit", "1", 1012)
    invoice.add_discount(Discount.percent("12.5"))
    assert totals_of(invoice) == (1012, 127, 89, 974)


def test_cli_export_and_summary_follow_rounding_rules(write_csv, run_ledger):
    path = write_csv(
        "R-8,2026-03-02,Acme,Cable,0.5,2.83,7.25,\n"
        "R-8,,,Clips,1.15,0.50,,\n"
        "R-9,2026-03-03,Birch,Kit,1,10.12,0,12.5%\n"
    )
    result = run_ledger("export", path)
    assert result.returncode == 0, result.stderr
    first, second = json.loads(result.stdout.decode("utf-8"))
    assert [line["amount_cents"] for line in first["lines"]] == [142, 58]
    assert (first["subtotal_cents"], first["discount_cents"], first["tax_cents"], first["total_cents"]) == (
        200,
        0,
        15,
        215,
    )
    assert (second["subtotal_cents"], second["discount_cents"], second["tax_cents"], second["total_cents"]) == (
        1012,
        127,
        0,
        885,
    )
    summary = run_ledger("summary", path)
    assert summary.returncode == 0, summary.stderr
    lines = summary.stdout.decode("utf-8").splitlines()
    assert lines[0].startswith("R-8") and lines[0].endswith(" 2.15")
    assert lines[1].startswith("R-9") and lines[1].endswith(" 8.85")
    assert lines[2].startswith("TOTAL") and lines[2].endswith(" 11.00")
