#!/bin/sh
# Mutant control: the reference's rule written nine times, one edit over the
# cap of 8. Admission requires it to fail.
exec python3 -I - <<'PY'
import json

edit = {
    "op": "add_item",
    "file": "@instructions",
    "section": "Checks",
    "text": "Run the project's tests once more after your last edit.",
    "evidence": ["dev:example#1/A"],
    "reason": "the cited run stopped after an edit without running the tests",
}
with open("edits.json", "w") as fh:
    json.dump({"rationale": "too many edits", "edits": [edit] * 9}, fh, indent=2)
PY
