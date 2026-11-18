#!/bin/sh
# Reference control: does the task the way a correct agent would. Admission
# runs it in the agent's place, in the workspace, and requires the verifier
# to pass it three times in a row.
exec python3 -I - <<'PY'
import csv
from collections import defaultdict

totals = defaultdict(lambda: [0, 0, 0.0])
with open("data/sales.csv", newline="") as f:
    for row in csv.DictReader(f):
        if row["status"] != "complete":
            continue
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
bars = []
for i, r in enumerate(regions):
    h = round(200 * totals[r][2] / top, 2)
    bars.append(f'  <rect x="{20 + i * 60}" y="{220 - h}" width="40" height="{h}" fill="#4a7bd0"><title>{r}</title></rect>')
with open("chart.svg", "w") as f:
    f.write('<svg xmlns="http://www.w3.org/2000/svg" width="280" height="240" viewBox="0 0 280 240">\n')
    f.write("\n".join(bars) + "\n</svg>\n")
PY
