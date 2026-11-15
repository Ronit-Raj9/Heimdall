#!/bin/sh
# Mutant control: reads the real forecasts but applies only the wind half of
# the rule, so the rainy depot flies. Admission requires it to fail.
exec python3 -I - <<'PY'
import json, os, urllib.request

apis = json.loads(os.environ.get("BENCH_EXTERNAL_APIS") or "{}")
URL = (apis.get("weather") or {}).get("url") or "http://toolproxy:9200/x/weather"
plan = {"date": "2026-11-16", "fly": [], "hold": []}
for sid in ("riverside", "hilltop", "quayside"):
    with urllib.request.urlopen(URL + "/forecast?site=%s&date=2026-11-16" % sid, timeout=30) as resp:
        fc = json.load(resp)
    slot = next(h for h in fc["hourly"] if h["time"] == "09:00")
    plan["fly" if slot["gust_kmh"] < 35 else "hold"].append(sid)
with open("plan.json", "w") as f:
    json.dump(plan, f)
PY
