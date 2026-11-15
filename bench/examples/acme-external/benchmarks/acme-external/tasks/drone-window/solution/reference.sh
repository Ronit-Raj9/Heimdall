#!/bin/sh
# Reference control: plans the way a correct dispatch agent would, from the
# forecasts it reads through the tool proxy (BENCH_EXTERNAL_APIS names the
# weather API's URL). Admission runs it in the agent's place, in replay mode,
# and requires the verifier to pass it three times in a row.
exec python3 -I - <<'PY'
import json, os, urllib.request

apis = json.loads(os.environ.get("BENCH_EXTERNAL_APIS") or "{}")
URL = (apis.get("weather") or {}).get("url") or "http://toolproxy:9200/x/weather"


def get(path):
    with urllib.request.urlopen(URL + path, timeout=30) as resp:
        return json.load(resp)


plan = {"date": "2026-11-16", "fly": [], "hold": []}
for site in get("/sites")["sites"]:
    fc = get("/forecast?site=%s&date=2026-11-16" % site["id"])
    slot = next(h for h in fc["hourly"] if h["time"] == "09:00")
    ok = slot["gust_kmh"] < 35 and slot["precip_prob"] < 50
    plan["fly" if ok else "hold"].append(site["id"])
with open("plan.json", "w") as f:
    json.dump(plan, f)
print(json.dumps(plan))
PY
