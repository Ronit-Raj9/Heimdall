#!/usr/bin/env python3
"""Verifier of the invoice-summary task (Acme Design example, fictional).

The bench copies the agent's summary.csv and summary.json out of the
workspace after the agent exits and mounts them read-only at
/grader/artifacts/summary_csv/summary.csv and
/grader/artifacts/summary_json/summary.json. The expected numbers are
written below, worked out once from the task's data/invoices.csv; this
script never reads the workspace. It writes /grader/out/reward.json:

  {"reward": 1, "dimensions": {"csv": 1, "json": 1}}

csv   the exact header, one row per client with a non-void invoice, sorted
      by client, counts equal, amounts within 0.005 and written with two
      decimals
json  an object whose total_eur and open_eur are within 0.005 and whose
      clients is the integer 4

The reward is 1 only when both dimensions are 1. A missing or unreadable
file scores 0 on its dimension.

Run it by hand:
  GRADER_ARTIFACTS=<folder holding summary_csv/ and summary_json/> GRADER_OUT=<folder> python3 verify.py
"""

import csv
import io
import json
import os
import re
import sys

ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
CSV_PATH = os.path.join(ARTIFACTS, "summary_csv", "summary.csv")
JSON_PATH = os.path.join(ARTIFACTS, "summary_json", "summary.json")

HEADER = ["client", "invoices", "total_eur", "open_eur"]
# client, non-void invoices, total of those, total of the open ones.
EXPECTED_ROWS = [
    ("Birchwood Bakery", 2, 1625.25, 375.25),
    ("Harbor Lane Books", 3, 2075.60, 1120.00),
    ("Marlow Cycle Works", 2, 2880.40, 730.00),
    ("Quillfeather Studio", 3, 4356.35, 3715.60),
]
EXPECTED_TOTAL = 10937.60
EXPECTED_OPEN = 5940.85
TOLERANCE = 0.005
AMOUNT_RE = re.compile(r"^-?[0-9]+\.[0-9]{2}$")


def read(path):
    with open(path, encoding="utf-8-sig") as fh:
        return fh.read()


def close(a, b):
    return abs(a - b) <= TOLERANCE


def check_csv():
    try:
        text = read(CSV_PATH)
    except (OSError, UnicodeDecodeError) as exc:
        return False, [f"cannot read summary.csv: {exc}"]
    rows = [r for r in csv.reader(io.StringIO(text)) if any(c.strip() for c in r)]
    if not rows:
        return False, ["summary.csv is empty"]
    header = [c.strip() for c in rows[0]]
    if header != HEADER:
        return False, [f"header is {header}, want {HEADER}"]
    body = rows[1:]
    notes = []
    if len(body) != len(EXPECTED_ROWS):
        notes.append(f"{len(body)} rows, want {len(EXPECTED_ROWS)}")
    for i, (got, want) in enumerate(zip(body, EXPECTED_ROWS), 1):
        got = [c.strip() for c in got]
        if len(got) != 4:
            notes.append(f"row {i}: {len(got)} fields, want 4")
            continue
        client, count, total, open_ = got
        if client != want[0]:
            notes.append(f"row {i}: client {client!r}, want {want[0]!r}")
            continue
        if count != str(want[1]):
            notes.append(f"{client}: invoices {count!r}, want {want[1]}")
        for name, value, expected in (("total_eur", total, want[2]), ("open_eur", open_, want[3])):
            if not AMOUNT_RE.match(value):
                notes.append(f"{client}: {name} {value!r} is not an amount with two decimals")
            elif not close(float(value), expected):
                notes.append(f"{client}: {name} {value}, want {expected:.2f}")
    return not notes, notes


def check_json():
    try:
        data = json.loads(read(JSON_PATH))
    except (OSError, UnicodeDecodeError, ValueError) as exc:
        return False, [f"cannot read summary.json: {exc}"]
    if not isinstance(data, dict):
        return False, ["summary.json is not an object"]
    notes = []
    for key, expected in (("total_eur", EXPECTED_TOTAL), ("open_eur", EXPECTED_OPEN)):
        value = data.get(key)
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            notes.append(f"{key} is {value!r}, want a number")
        elif not close(float(value), expected):
            notes.append(f"{key} {value}, want {expected:.2f}")
    clients = data.get("clients")
    if isinstance(clients, bool) or not isinstance(clients, int) or clients != len(EXPECTED_ROWS):
        notes.append(f"clients is {clients!r}, want {len(EXPECTED_ROWS)}")
    return not notes, notes


def main():
    dims = {}
    for name, check in (("csv", check_csv), ("json", check_json)):
        ok, notes = check()
        dims[name] = int(ok)
        print(f"{name}:", "ok" if ok else "; ".join(notes))
    reward = 1 if all(dims.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as fh:
        json.dump({"reward": reward, "dimensions": dims}, fh)
    print(json.dumps({"reward": reward, "dimensions": dims}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
