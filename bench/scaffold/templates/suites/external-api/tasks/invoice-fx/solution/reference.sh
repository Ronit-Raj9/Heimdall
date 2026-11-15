#!/bin/sh
# Reference control: does the task the way a correct agent would, through the
# tool proxy (BENCH_EXTERNAL_APIS names the fx API's URL). Admission runs it
# in the agent's place, in replay mode, and requires the verifier to pass it
# three times in a row.
exec python3 -I - <<'PY'
import json, os, urllib.request

apis = json.loads(os.environ.get("BENCH_EXTERNAL_APIS") or "{}")
URL = (apis.get("fx") or {}).get("url") or "http://toolproxy:9200/x/fx"
with urllib.request.urlopen(URL + "/rates?base=EUR&symbols=USD,JPY", timeout=30) as resp:
    data = json.load(resp)
eur = 1250.00
quote = {
    "invoice": "INV-2041",
    "eur": eur,
    "usd": round(eur * data["rates"]["USD"], 2),
    "jpy": int(round(eur * data["rates"]["JPY"])),
    "rate_date": data["date"],
}
with open("quote.json", "w") as f:
    json.dump(quote, f)
print(json.dumps(quote))
PY
