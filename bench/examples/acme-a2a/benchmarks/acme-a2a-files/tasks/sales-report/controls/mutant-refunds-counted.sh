#!/bin/sh
# Mutant control: a plausible near miss. It counts the refunded orders too,
# so North and East come out too high in both files. Admission requires it
# to fail.
exec python3 -I - <<'PY'
import csv
from collections import defaultdict

totals = defaultdict(lambda: [0, 0, 0.0])
with open("data/sales.csv", newline="") as f:
    for row in csv.DictReader(f):
        t = totals[row["region"]]
        t[0] += 1
        t[1] += int(row["units"])
        t[2] += int(row["units"]) * float(row["unit_price"])

regions = sorted(totals)
with open("report.csv", "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["region", "orders", "units", "revenue"])
    for r in regions:
        orders, units, revenue = totals[r]
        w.writerow([r, orders, units, f"{revenue:.2f}"])

top = max(t[2] for t in totals.values())
bars = [f'<rect x="{20 + i * 60}" y="0" width="40" height="{200 * totals[r][2] / top:.2f}"><title>{r}</title></rect>'
        for i, r in enumerate(regions)]
with open("chart.svg", "w") as f:
    f.write('<svg xmlns="http://www.w3.org/2000/svg" width="280" height="240">' + "".join(bars) + "</svg>\n")
PY
