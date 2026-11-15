#!/bin/sh
# Reference control: summarises data/invoices.csv the way a correct agent
# would. Admission runs it in the agent's place and requires the verifier to
# pass the two files it leaves, three times in a row.
exec python3 -I - <<'PY'
import csv
import json
from decimal import Decimal

clients = {}
with open("data/invoices.csv", newline="") as fh:
    for row in csv.DictReader(fh):
        if row["status"] == "void":
            continue
        c = clients.setdefault(row["client"], {"invoices": 0, "total": Decimal("0"), "open": Decimal("0")})
        amount = Decimal(row["amount_eur"])
        c["invoices"] += 1
        c["total"] += amount
        if row["status"] == "open":
            c["open"] += amount

with open("summary.csv", "w", newline="") as fh:
    w = csv.writer(fh, lineterminator="\n")
    w.writerow(["client", "invoices", "total_eur", "open_eur"])
    for name in sorted(clients):
        c = clients[name]
        w.writerow([name, c["invoices"], f"{c['total']:.2f}", f"{c['open']:.2f}"])

total = sum(c["total"] for c in clients.values())
open_ = sum(c["open"] for c in clients.values())
with open("summary.json", "w") as fh:
    json.dump({"total_eur": float(total), "open_eur": float(open_), "clients": len(clients)}, fh)
    fh.write("\n")
PY
