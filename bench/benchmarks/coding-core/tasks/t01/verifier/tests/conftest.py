"""Hidden-test setup: import the package under test from /workspace.

The grader runs ``python -I``, so neither the current directory nor
PYTHONPATH is on sys.path. Put the workspace first.
"""

import os
import subprocess
import sys

import pytest

WORKSPACE = os.environ.get("LEDGER_WORKSPACE", "/workspace")
sys.path.insert(0, WORKSPACE)

HEADER = "invoice,date,customer,description,quantity,unit_price,tax_rate,discount\n"


@pytest.fixture
def write_csv(tmp_path):
    """Write a CSV of line items (header added) and return its path."""

    def write(body, name="items.csv", header=HEADER):
        path = tmp_path / name
        path.write_bytes((header + body).encode("utf-8"))
        return path

    return write


@pytest.fixture
def run_ledger(tmp_path):
    """Run ``python -m ledger ARGS`` against the workspace; stdout and stderr are bytes."""

    def run(*args):
        env = dict(os.environ, PYTHONPATH=WORKSPACE)
        return subprocess.run(
            [sys.executable, "-m", "ledger", *[str(a) for a in args]],
            cwd=tmp_path,
            env=env,
            capture_output=True,
            timeout=60,
        )

    return run
