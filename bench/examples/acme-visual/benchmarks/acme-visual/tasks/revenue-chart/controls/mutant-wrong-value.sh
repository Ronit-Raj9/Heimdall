#!/bin/sh
# Mutant: Q2 drawn as 160 instead of 180, a misread figure (the reference
# script with that one mistake). The structure check passes; the pixel check
# must fail it.
exec python3 -I - wrong-value <<'PY'
import csv, os, sys

mutate = sys.argv[1]
rows = list(csv.DictReader(open("data/revenue.csv")))
font = 'font-family="Liberation Sans"'
out = ['<svg xmlns="http://www.w3.org/2000/svg" width="480" height="320" viewBox="0 0 480 320">',
       '  <rect x="0" y="0" width="480" height="320" fill="#ffffff"/>',
       f'  <text x="240" y="32" {font} font-size="18" font-weight="bold" fill="#111827" text-anchor="middle">Quarterly revenue (k$)</text>']
for i, row in enumerate(rows):
    value = int(row["revenue_k"])
    if mutate == "wrong-value" and row["quarter"] == "Q2":
        value = 160
    x = 60 + i * 100
    cx = x + 30
    out.append(f'  <rect x="{x}" y="{270 - value}" width="60" height="{value}" fill="#2563eb"/>')
    if not (mutate == "missing-label" and row["quarter"] == "Q3"):
        out.append(f'  <text x="{cx}" y="292" {font} font-size="14" fill="#374151" text-anchor="middle">{row["quarter"]}</text>')
    out.append(f'  <text x="{cx}" y="{270 - value - 8}" {font} font-size="13" fill="#111827" text-anchor="middle">{value}</text>')
out.append('  <line x1="40" y1="270" x2="450" y2="270" stroke="#6b7280" stroke-width="2"/>')
out.append("</svg>")
os.makedirs("chart", exist_ok=True)
with open("chart/revenue.svg", "w") as fh:
    fh.write("\n".join(out) + "\n")
PY
