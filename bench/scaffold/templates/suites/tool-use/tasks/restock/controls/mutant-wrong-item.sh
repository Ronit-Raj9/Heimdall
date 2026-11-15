#!/bin/sh
# Mutant control: a plausible wrong action, recording the delivery on the 500 ml
# bottle (W-101) instead of the 750 ml one. Admission requires it to fail,
# which shows the checks catch a near miss. It runs in the agent's place
# with the services up.
exec python3 -I - <<'PY'
import json, os, sys, urllib.error, urllib.request

URL = os.environ.get("TOOL_URL") or "http://toolproxy:9200/s/inventory"


def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(URL.rstrip("/") + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path}: {e.code} {e.read().decode()}")


item = call("GET", "/items/W-101")
call("POST", f"/items/{item['sku']}/adjust", {"delta": 12, "reason": "Delivery D-5531 from supplier"})
print("recorded 12 units of W-101 for delivery D-5531")
PY
