EXAMPLE_ROWS = (
    "INV-1001,2026-03-02,Northwind Traders,Website audit,1,1200.00,8.875,\n"
    "INV-1001,,,Follow-up calls,2.5,80.00,,\n"
    'INV-1002,2026-03-05,"Hale, Brook & Sons",Annual support plan,1,2400.00,0,10%\n'
    "INV-1003,2026-03-09,Juniper Labs,Data cleanup,12,45.00,7.25,50.00\n"
    "INV-1003,,,Report,1,150.00,,\n"
)

EXAMPLE_CSV = (
    b"invoice,date,customer,lines,subtotal,discount,tax,total\n"
    b"INV-1001,2026-03-02,Northwind Traders,2,1400.00,0.00,124.25,1524.25\n"
    b'INV-1002,2026-03-05,"Hale, Brook & Sons",1,2400.00,240.00,0.00,2160.00\n'
    b"INV-1003,2026-03-09,Juniper Labs,2,690.00,50.00,46.40,686.40\n"
)

JSON_ROWS = (
    "J-1,2026-04-01,Quill & Ink,Paper,3,4.25,5,\n"
    "J-1,,,Toner,1,60.00,,\n"
    'J-2,2026-04-02,"Moss, Fern",Seeds,0.5,9.99,0,15%\n'
)

EXPECTED_JSON = (
    b'[\n  {\n    "invoice": "J-1",\n    "date": "2026-04-01",\n    "customer": "Quill & Ink",\n'
    b'    "tax_rate": "5",\n    "discount": null,\n    "lines": [\n      {\n'
    b'        "description": "Paper",\n        "quantity": "3",\n        "unit_price_cents": 425,\n'
    b'        "amount_cents": 1275\n      },\n      {\n        "description": "Toner",\n'
    b'        "quantity": "1",\n        "unit_price_cents": 6000,\n        "amount_cents": 6000\n'
    b'      }\n    ],\n    "subtotal_cents": 7275,\n    "discount_cents": 0,\n    "tax_cents": 364,\n'
    b'    "total_cents": 7639\n  },\n  {\n    "invoice": "J-2",\n    "date": "2026-04-02",\n'
    b'    "customer": "Moss, Fern",\n    "tax_rate": "0",\n    "discount": "15%",\n    "lines": [\n'
    b'      {\n        "description": "Seeds",\n        "quantity": "0.5",\n'
    b'        "unit_price_cents": 999,\n        "amount_cents": 500\n      }\n    ],\n'
    b'    "subtotal_cents": 500,\n    "discount_cents": 75,\n    "tax_cents": 0,\n'
    b'    "total_cents": 425\n  }\n]\n'
)


def test_csv_export_matches_documented_example(write_csv, run_ledger):
    path = write_csv(EXAMPLE_ROWS)
    result = run_ledger("export", path, "--format", "csv")
    assert result.returncode == 0, result.stderr
    assert result.stdout == EXAMPLE_CSV


def test_csv_quotes_only_fields_that_need_it(write_csv, run_ledger):
    path = write_csv(
        'Q-1,2026-05-01,"Ann ""Bo"" Lee",Lamp,2,12.50,10,\n'
        'Q-2,2026-05-02,"Smith, Jones",Desk,1,300.00,0,25.00\n'
        "Q-3,2026-05-03,O'Hara & Co,Chair,4,75.00,0,\n"
    )
    result = run_ledger("export", path, "--format", "csv")
    assert result.returncode == 0, result.stderr
    assert result.stdout.decode("utf-8").split("\n") == [
        "invoice,date,customer,lines,subtotal,discount,tax,total",
        'Q-1,2026-05-01,"Ann ""Bo"" Lee",1,25.00,0.00,2.50,27.50',
        'Q-2,2026-05-02,"Smith, Jones",1,300.00,25.00,0.00,275.00',
        "Q-3,2026-05-03,O'Hara & Co,1,300.00,0.00,0.00,300.00",
        "",
    ]


def test_csv_rows_end_with_a_single_line_feed(write_csv, run_ledger):
    path = write_csv(EXAMPLE_ROWS)
    out = run_ledger("export", path, "--format", "csv").stdout
    assert b"\r" not in out
    assert out.endswith(b"\n") and not out.endswith(b"\n\n")
    assert out.count(b"\n") == 4
    assert b"\n\n" not in out


def test_json_output_is_unchanged(write_csv, run_ledger):
    path = write_csv(JSON_ROWS)
    default = run_ledger("export", path)
    explicit = run_ledger("export", path, "--format", "json")
    assert default.returncode == 0, default.stderr
    assert explicit.returncode == 0, explicit.stderr
    assert default.stdout == EXPECTED_JSON
    assert explicit.stdout == EXPECTED_JSON


def test_output_option_writes_the_same_bytes(write_csv, run_ledger, tmp_path):
    path = write_csv(EXAMPLE_ROWS)
    for fmt in ("csv", "json"):
        target = tmp_path / f"out.{fmt}"
        to_file = run_ledger("export", path, "--format", fmt, "-o", target)
        assert to_file.returncode == 0, to_file.stderr
        assert to_file.stdout == b""
        assert target.read_bytes() == run_ledger("export", path, "--format", fmt).stdout
    target = tmp_path / "long.csv"
    long_form = run_ledger("export", path, "--output", target, "--format", "csv")
    assert long_form.returncode == 0, long_form.stderr
    assert target.read_bytes() == EXAMPLE_CSV


def test_unknown_format_is_a_usage_error(write_csv, run_ledger):
    path = write_csv(EXAMPLE_ROWS)
    result = run_ledger("export", path, "--format", "xml")
    assert result.returncode == 2
    assert result.stdout == b""


def test_header_only_input_gives_just_the_header_row(write_csv, run_ledger):
    path = write_csv("")
    result = run_ledger("export", path, "--format", "csv")
    assert result.returncode == 0, result.stderr
    assert result.stdout == b"invoice,date,customer,lines,subtotal,discount,tax,total\n"
