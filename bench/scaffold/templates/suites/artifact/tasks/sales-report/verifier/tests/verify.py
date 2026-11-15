"""Artifact verifier of the sales-report task.

Written by `bifrost-bench suite init -template artifact`. It runs after the
agent exited, with no network, in a fresh container from the suite's base
image. The bench copied the task's artifacts out of the workspace and mounts
them read-only at /grader/artifacts/<artifact id>/<file name>; this folder
is at /grader/tests. It never reads the workspace and never trusts a reward
file the agent wrote: the only result is the one it writes to
/grader/out/reward.json:

  {"reward": 1, "dimensions": {"report": 1, "chart": 1}}

The reward is 1 only when every dimension is 1. Each dimension says which
artifact fell short; the reasons are printed to stdout, which the bench
keeps with the grade. To make it yours, change EXPECTED and the two check
functions. The expected values live here, in the hidden folder, not in the
workspace the agent sees.

Run it by hand against a folder holding report/report.csv and
chart/chart.svg:
  GRADER_ARTIFACTS=<folder> GRADER_OUT=<folder> python3 verify.py
"""

import csv
import json
import os
import xml.etree.ElementTree as ET

ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")

# Completed orders of data/sales.csv per region, sorted by region:
# (region, orders, units, revenue). Refunded orders are left out.
EXPECTED = [
    ("East", 2, 8, 267.00),
    ("North", 3, 13, 335.97),
    ("South", 3, 10, 352.46),
    ("West", 3, 19, 402.43),
]
HEADER = ["region", "orders", "units", "revenue"]
MONEY_TOLERANCE = 0.005  # half a cent
HEIGHT_TOLERANCE = 0.02  # bar height relative to the tallest bar


def artifact(artifact_id, name):
    return os.path.join(ARTIFACTS, artifact_id, name)


def check_report(path):
    """report.csv: the header, then one row per region with exact counts."""
    try:
        with open(path, newline="", encoding="utf-8") as f:
            rows = [r for r in csv.reader(f) if any(c.strip() for c in r)]
    except (OSError, UnicodeDecodeError, csv.Error) as e:
        return False, f"cannot read report.csv: {e}"
    if not rows or [c.strip().lower() for c in rows[0]] != HEADER:
        return False, f"header is {rows[0] if rows else 'missing'}, want {','.join(HEADER)}"
    body = rows[1:]
    if len(body) != len(EXPECTED):
        return False, f"{len(body)} rows, want {len(EXPECTED)} (one per region)"
    for row, (region, orders, units, revenue) in zip(body, EXPECTED):
        if len(row) != len(HEADER):
            return False, f"row {row} does not have {len(HEADER)} columns"
        got_region = row[0].strip()
        try:
            got_orders, got_units, got_revenue = int(row[1]), int(row[2]), float(row[3])
        except ValueError:
            return False, f"row {row}: orders and units must be integers, revenue a number"
        if got_region != region:
            return False, f"row for {got_region!r} where {region!r} was expected (sorted by region)"
        if (got_orders, got_units) != (orders, units):
            return False, f"{region}: orders {got_orders}, units {got_units}; want {orders} and {units}"
        if abs(got_revenue - revenue) > MONEY_TOLERANCE:
            return False, f"{region}: revenue {got_revenue}, want {revenue:.2f}"
    return True, "report.csv matches"


def local(tag):
    return tag.rsplit("}", 1)[-1]


def number(value):
    return float(str(value).strip().removesuffix("px"))


def check_chart(path):
    """chart.svg: well formed, one titled bar per region, heights in proportion."""
    try:
        root = ET.parse(path).getroot()
    except (OSError, ET.ParseError) as e:
        return False, f"chart.svg is not well-formed XML: {e}"
    if local(root.tag) != "svg":
        return False, f"root element is <{local(root.tag)}>, want <svg>"
    bars = {}
    for el in root.iter():
        if local(el.tag) != "rect":
            continue
        title = next((c for c in el if local(c.tag) == "title"), None)
        if title is None or not (title.text or "").strip():
            continue
        name = title.text.strip()
        if name in bars:
            return False, f"two bars titled {name!r}"
        try:
            bars[name] = number(el.get("height", ""))
        except ValueError:
            return False, f"bar {name!r} has no numeric height"
    want = {region: revenue for region, _, _, revenue in EXPECTED}
    if set(bars) != set(want):
        return False, f"bars titled {sorted(bars)}, want one per region {sorted(want)}"
    tallest = max(bars.values())
    if tallest <= 0 or min(bars.values()) <= 0:
        return False, "every bar needs a positive height"
    top = max(want.values())
    for region, revenue in want.items():
        got, expect = bars[region] / tallest, revenue / top
        if abs(got - expect) > HEIGHT_TOLERANCE:
            return False, f"bar {region!r} is {got:.3f} of the tallest, want {expect:.3f}"
    return True, "chart.svg matches"


def main():
    checks = {
        "report": (check_report, artifact("report", "report.csv")),
        "chart": (check_chart, artifact("chart", "chart.svg")),
    }
    dims = {}
    for name, (fn, path) in checks.items():
        if not os.path.isfile(path):
            ok, why = False, f"{path} is missing"
        else:
            ok, why = fn(path)
        dims[name] = 1 if ok else 0
        print(f"{name}: {'pass' if ok else 'fail'}: {why}")
    reward = 1 if all(dims.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as f:
        json.dump({"reward": reward, "dimensions": dims}, f)
    print(f"reward {reward}")


if __name__ == "__main__":
    main()
