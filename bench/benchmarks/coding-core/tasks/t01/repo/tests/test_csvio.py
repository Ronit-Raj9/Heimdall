from datetime import date

import pytest

from ledger import CsvFormatError, load_invoices

HEADER = "invoice,date,customer,description,quantity,unit_price,tax_rate,discount\n"


def write(tmp_path, body):
    path = tmp_path / "items.csv"
    path.write_text(HEADER + body, encoding="utf-8")
    return path


def test_rows_group_into_invoices(tmp_path):
    path = write(
        tmp_path,
        "A-1,2026-01-05,Acme,Setup,1,100.00,10,\n"
        "B-2,2026-01-06,Birch Co,Support,2,50.00,,\n"
        "A-1,,,Training,3,20.00,,\n",
    )
    invoices = load_invoices(path)
    assert [inv.number for inv in invoices] == ["A-1", "B-2"]
    first = invoices[0]
    assert first.issued == date(2026, 1, 5)
    assert first.customer == "Acme"
    assert [item.description for item in first.lines] == ["Setup", "Training"]
    assert first.totals().total == 17600


def test_missing_column_is_reported_on_line_1(tmp_path):
    path = tmp_path / "items.csv"
    path.write_text("invoice,date,customer,description,unit_price\n", encoding="utf-8")
    with pytest.raises(CsvFormatError, match=r"^line 1: missing column\(s\): quantity"):
        load_invoices(path)


def test_bad_quantity_reports_line_number(tmp_path):
    path = write(tmp_path, "A-1,2026-01-05,Acme,Setup,1,100.00,,\nA-1,,,Extra,lots,1.00,,\n")
    with pytest.raises(CsvFormatError, match=r"^line 3: invalid quantity 'lots'"):
        load_invoices(path)


def test_bad_date_is_rejected(tmp_path):
    path = write(tmp_path, "A-1,2026-13-05,Acme,Setup,1,100.00,,\n")
    with pytest.raises(CsvFormatError, match=r"^line 2: invalid date"):
        load_invoices(path)


def test_conflicting_customer_is_rejected(tmp_path):
    path = write(tmp_path, "A-1,2026-01-05,Acme,Setup,1,100.00,,\nA-1,,Acme Ltd,Extra,1,1.00,,\n")
    with pytest.raises(CsvFormatError, match=r"^line 3: invoice A-1: customer"):
        load_invoices(path)
