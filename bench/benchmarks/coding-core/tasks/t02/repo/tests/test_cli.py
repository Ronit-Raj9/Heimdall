import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EXAMPLE = ROOT / "examples" / "invoices.csv"


def run(*args):
    env = dict(os.environ, PYTHONPATH=str(ROOT))
    return subprocess.run(
        [sys.executable, "-m", "ledger", *args], cwd=ROOT, env=env, capture_output=True, text=True, timeout=60
    )


def test_summary_lists_each_invoice():
    result = run("summary", str(EXAMPLE))
    assert result.returncode == 0, result.stderr
    lines = result.stdout.splitlines()
    assert lines[0].startswith("INV-1001")
    assert lines[0].endswith("1524.25")
    assert lines[-2].startswith("TOTAL")
    assert lines[-1] == "3 invoice(s)"


def test_export_json_has_totals():
    result = run("export", str(EXAMPLE))
    assert result.returncode == 0, result.stderr
    data = json.loads(result.stdout)
    assert [inv["invoice"] for inv in data] == ["INV-1001", "INV-1002", "INV-1003"]
    second = data[1]
    assert second["customer"] == "Hale, Brook & Sons"
    assert second["discount"] == "10%"
    assert (second["subtotal_cents"], second["discount_cents"], second["tax_cents"]) == (240000, 24000, 0)
    assert second["total_cents"] == 216000


def test_missing_file_exits_1(tmp_path):
    result = run("summary", str(tmp_path / "nope.csv"))
    assert result.returncode == 1
    assert result.stderr.startswith("error: cannot read")
