import json
from datetime import date

import pytest

from ledger import CsvFormatError, load_invoices


def import_error(write_csv, body):
    path = write_csv(body)
    with pytest.raises(CsvFormatError) as info:
        load_invoices(path)
    return str(info.value)


def test_day_first_slash_dates_are_accepted(write_csv):
    path = write_csv(
        "D-1,07/03/2026,Acme,Setup,1,10.00,,\n"
        "D-2,7/3/2026,Acme,Setup,1,10.00,,\n"
        "D-3,07/3/2026,Acme,Setup,1,10.00,,\n"
        "D-4,03/04/2026,Acme,Setup,1,10.00,,\n"
        "D-5,12/11/2026,Acme,Setup,1,10.00,,\n"
        "D-6, 31/12/2026 ,Acme,Setup,1,10.00,,\n"
    )
    issued = {inv.number: inv.issued for inv in load_invoices(path)}
    assert issued == {
        "D-1": date(2026, 3, 7),
        "D-2": date(2026, 3, 7),
        "D-3": date(2026, 3, 7),
        "D-4": date(2026, 4, 3),
        "D-5": date(2026, 11, 12),
        "D-6": date(2026, 12, 31),
    }
    assert all(type(value) is date for value in issued.values())


def test_iso_dates_are_still_accepted(write_csv):
    path = write_csv("I-1,2026-03-07,Acme,Setup,1,10.00,,\nI-2, 2026-12-31 ,Acme,Setup,1,10.00,,\n")
    assert [inv.issued for inv in load_invoices(path)] == [date(2026, 3, 7), date(2026, 12, 31)]


def test_two_digit_year_is_ambiguous(write_csv):
    message = import_error(
        write_csv,
        "A-1,2026-03-07,Acme,Setup,1,10.00,,\nA-2,07/03/26,Birch,Setup,1,10.00,,\n",
    )
    assert message.startswith("line 3:")
    assert "ambiguous" in message
    assert "07/03/26" in message


def test_month_first_slash_date_is_invalid(write_csv):
    message = import_error(write_csv, "M-1,03/15/2026,Acme,Setup,1,10.00,,\n")
    assert message.startswith("line 2:")
    assert "invalid date" in message
    assert "03/15/2026" in message


def test_malformed_and_impossible_dates_are_invalid(write_csv):
    for value in ("31/04/2026", "29/02/2025", "2026/03/07", "07-03-2026", "7.3.2026", "2026-3-7", "2026-02-30"):
        message = import_error(write_csv, f"X-1,{value},Acme,Setup,1,10.00,,\n")
        assert message.startswith("line 2:"), value
        assert "invalid date" in message, value
        assert value in message, value


def test_same_date_in_both_formats_agrees(write_csv):
    path = write_csv(
        "S-1,2026-03-07,Acme,Setup,1,10.00,,\n"
        "S-1,07/03/2026,,Training,2,5.00,,\n"
        "S-1,7/3/2026,,Report,1,1.00,,\n"
        "S-2,08/03/2026,Birch,Setup,1,10.00,,\n"
        "S-2,2026-03-08,,Extra,1,1.00,,\n"
    )
    first, second = load_invoices(path)
    assert (first.issued, len(first.lines)) == (date(2026, 3, 7), 3)
    assert (second.issued, len(second.lines)) == (date(2026, 3, 8), 2)


def test_different_date_on_later_row_still_conflicts(write_csv):
    message = import_error(
        write_csv,
        "C-1,2026-03-07,Acme,Setup,1,10.00,,\nC-1,08/03/2026,,Training,1,5.00,,\n",
    )
    assert message.startswith("line 3:")


def test_cli_exports_iso_dates_and_rejects_ambiguous_ones(write_csv, run_ledger):
    good = write_csv("E-1,9/3/2026,Acme,Setup,1,10.00,,\nE-2,2026-03-10,Birch,Setup,1,10.00,,\n")
    result = run_ledger("export", good)
    assert result.returncode == 0, result.stderr
    assert [inv["date"] for inv in json.loads(result.stdout.decode("utf-8"))] == ["2026-03-09", "2026-03-10"]
    bad = write_csv("E-3,09/03/26,Acme,Setup,1,10.00,,\n", name="bad.csv")
    failed = run_ledger("export", bad)
    assert failed.returncode == 1
