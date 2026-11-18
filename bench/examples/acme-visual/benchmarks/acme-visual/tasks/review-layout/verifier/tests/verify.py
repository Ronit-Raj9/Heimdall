"""State check of the review-layout task (hidden).

Reads the desktop's state dump at /grader/state/desktop/state.json and
writes /grader/out/reward.json with one dimension per check: the note is
unchanged on disk and the editor is still running. Whether line numbers
and the status bar are on is checked by the visual grader, not here: the
editor keeps that setting in memory only. To run it by hand:

  GRADER_STATE=<folder holding desktop/state.json> GRADER_OUT=<folder> python3 verify.py
"""
import json
import os

SEED = "Release checklist\n\nOwner: Dana\nDue: Friday\n"
STATE_ROOT = os.environ.get("GRADER_STATE", "/grader/state")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
dims = {"note_unchanged": 0.0, "editor_running": 0.0}
try:
    with open(os.path.join(STATE_ROOT, "desktop", "state.json"), encoding="utf-8") as fh:
        state = json.load(fh)
    note = (state.get("fs") or {}).get("note.txt") or {}
    dims["note_unchanged"] = 1.0 if note.get("text") == SEED and state.get("paths") == ["note.txt"] else 0.0
    dims["editor_running"] = 1.0 if (state.get("app") or {}).get("running") is True else 0.0
except (OSError, ValueError):
    pass
os.makedirs(OUT_DIR, exist_ok=True)
with open(os.path.join(OUT_DIR, "reward.json"), "w") as fh:
    json.dump({"reward": min(dims.values()), "dimensions": dims}, fh)
