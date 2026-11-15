#!/bin/sh
# Mutant control: calls the API correctly but reads the rate the wrong way
# round (divides instead of multiplying), a plausible agent mistake.
# Admission requires it to fail.
exec python3 -I - <<'PY'
import json, os, urllib.request

apis = json.loads(os.environ.get("BENCH_EXTERNAL_APIS") or "{}")
URL = (apis.get("fx") or {}).get("url") or "http://toolproxy:9200/x/fx"
with urllib.request.urlopen(URL + "/rates?base=EUR&symbols=USD,JPY", timeout=30) as resp:
    data = json.load(resp)
eur = 1250.00
quote = {"invoice": "INV-2041", "eur": eur, "usd": round(eur / data["rates"]["USD"], 2),
         "jpy": int(round(eur / data["rates"]["JPY"])), "rate_date": data["date"]}
with open("quote.json", "w") as f:
    json.dump(quote, f)
PY
