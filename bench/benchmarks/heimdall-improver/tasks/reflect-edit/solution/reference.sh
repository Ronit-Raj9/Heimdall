#!/bin/sh
# Reference control: writes one valid add_item edit that cites the first
# practice run of evidence.json (or a placeholder run when admission runs
# without evidence). Admission requires the verifier to pass it.
exec python3 -I - <<'PY'
import json

ref = "dev:example#1/A"
try:
    with open("/heimdall/improve/evidence.json") as fh:
        items = json.load(fh).get("items") or []
    if items and items[0].get("run_ref"):
        ref = items[0]["run_ref"]
except (OSError, ValueError):
    pass

edits = {
    "rationale": "Runs end without running the tests once more after the last edit.",
    "edits": [
        {
            "op": "add_item",
            "file": "@instructions",
            "section": "Checks",
            "text": "Run the project's tests once more after your last edit and read every failure before finishing.",
            "evidence": [ref],
            "predicts_fixed": [],
            "reason": "the cited run stopped after an edit without running the tests",
        }
    ],
}
with open("edits.json", "w") as fh:
    json.dump(edits, fh, indent=2)
PY
