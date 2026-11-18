#!/bin/sh
# Reference control: does the task the way a correct agent would, through the
# same tool server the agent gets (via the per-run tool proxy). It runs in
# the agent's place with the services up; admission requires the verifier to
# pass it three times in a row.
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


item = call("GET", "/items/W-100")
call("POST", f"/items/{item['sku']}/adjust", {"delta": 12, "reason": "Delivery D-5531 from supplier"})
print("recorded 12 units of W-100 for delivery D-5531")
PY
