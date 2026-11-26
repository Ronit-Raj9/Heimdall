#!/usr/bin/env python3
"""Verifier of the reflect-edit task (the improver of improvement runs).

The improver's edits.json is copied out of the workspace after the agent
exits and mounted read-only at /grader/artifacts/edits/edits.json. This
script checks its form only and writes /grader/out/reward.json:

  {"reward": 1, "dimensions": {"form": 1}}

form  valid JSON of the edits schema: an object with a string rationale and
      a list of at most 8 edits; each edit a known op with a string reason;
      file and path absolute (or "@instructions" for file) and outside
      system folders; text under the size caps; evidence and predicts_fixed
      lists of strings.

Whether each cited run exists is checked when the edits are applied, since
this script never sees the evidence.

Run it by hand:
  GRADER_ARTIFACTS=<folder holding edits/> GRADER_OUT=<folder> python3 verify.py
"""

import json
import os
import sys

ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
EDITS_PATH = os.path.join(ARTIFACTS, "edits", "edits.json")

OPS = {"add_item", "edit_item", "delete_item", "add_skill", "edit_skill", "retire_skill", "rewrite_file"}
MAX_EDITS = 8
ITEM_WORDS = 120
SKILL_WORDS = 600
FILE_WORDS = 3000
SYSTEM = ("/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/proc", "/sys", "/dev", "/root", "/var", "/boot", "/grader", "/heimdall", "/opt")


def words(s):
    return len(s.split())


def check_path(p, field, allow_instructions):
    if allow_instructions and p == "@instructions":
        return None
    if not p.startswith("/"):
        return f"{field} {p!r} is not absolute"
    if ".." in p.split("/"):
        return f"{field} {p!r} climbs out of its folder"
    for s in SYSTEM:
        if p == s or p.startswith(s + "/"):
            return f"{field} {p!r} is in a system folder"
    return None


def check_edit(i, e):
    if not isinstance(e, dict):
        return [f"edit {i} is not an object"]
    notes = []
    op = e.get("op")
    if op not in OPS:
        notes.append(f"edit {i}: unknown op {op!r}")
    if not isinstance(e.get("reason", ""), str):
        notes.append(f"edit {i}: reason is not a string")
    for field in ("id", "file", "section", "path", "text"):
        if field in e and not isinstance(e[field], str):
            notes.append(f"edit {i}: {field} is not a string")
    for field in ("evidence", "predicts_fixed"):
        v = e.get(field, [])
        if not isinstance(v, list) or not all(isinstance(x, str) for x in v):
            notes.append(f"edit {i}: {field} is not a list of strings")
    if notes:
        return notes
    if e.get("file"):
        n = check_path(e["file"], "file", True)
        if n:
            notes.append(f"edit {i}: {n}")
    if e.get("path"):
        n = check_path(e["path"], "path", False)
        if n:
            notes.append(f"edit {i}: {n}")
    text = e.get("text", "")
    cap = {"add_item": ITEM_WORDS, "edit_item": ITEM_WORDS, "add_skill": SKILL_WORDS, "edit_skill": SKILL_WORDS}.get(op, FILE_WORDS)
    if words(text) > cap:
        notes.append(f"edit {i}: text is {words(text)} words, over {cap}")
    if op in ("add_item", "rewrite_file") and not e.get("file"):
        notes.append(f"edit {i}: {op} needs a file")
    if op in ("add_skill", "edit_skill", "retire_skill") and not e.get("path"):
        notes.append(f"edit {i}: {op} needs a path")
    if op in ("edit_item", "delete_item") and not e.get("id"):
        notes.append(f"edit {i}: {op} needs an id")
    return notes


def check():
    try:
        with open(EDITS_PATH, encoding="utf-8") as fh:
            doc = json.load(fh)
    except (OSError, UnicodeDecodeError, ValueError) as exc:
        return [f"cannot read edits.json: {exc}"]
    if not isinstance(doc, dict):
        return ["edits.json is not an object"]
    if not isinstance(doc.get("rationale", ""), str) or not isinstance(doc.get("no_edits", ""), str):
        return ["rationale and no_edits must be strings"]
    edits = doc.get("edits", [])
    if not isinstance(edits, list):
        return ["edits is not a list"]
    if len(edits) > MAX_EDITS:
        return [f"{len(edits)} edits, at most {MAX_EDITS}"]
    notes = []
    for i, e in enumerate(edits, 1):
        notes.extend(check_edit(i, e))
    return notes


def main():
    notes = check()
    ok = 1 if not notes else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w") as fh:
        json.dump({"reward": ok, "dimensions": {"form": ok}}, fh)
    for n in notes:
        print(n, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
