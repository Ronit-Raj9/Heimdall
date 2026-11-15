#!/usr/bin/env python3
"""State verifier of a Heimdall tool-use or mcp task.

Written by `bifrost-bench suite init` / `task init`. It needs no change: the
task's checks live in checks.py beside it. It runs after the agent exited,
with no network, in a fresh container from the suite's base image. The
service state the bench exported is mounted read-only at
/grader/state/<service>/<export>, this folder at /grader/tests, and the
reward goes to /grader/out/reward.json:

  {"reward": 1, "dimensions": {"<check name>": 1, ...}}

The reward is 1 only when every check passes; each check is also reported
as a dimension (never blended into the reward). A state file that is missing
or not JSON is not a task failure: the script exits 2 without a reward, so
the grade is reported as an evaluator problem instead.

checks.py defines STATE (the export to read, "<service>/<export>") and
CHECKS, a list of dicts. Each check has a "name", a "path" into the state,
and one test:

  path        "items/sku=W-100/stock": keys and list indexes split by "/";
              "key=value" picks the first list element whose key equals value
  equals      the value equals this (JSON types compared exactly)
  not_equals  the value differs from this
  in          the value is one of this list
  contains    a string contains this text, or a list holds this element
  not_contains
  matches     a string matches this regular expression (re.search)
  length      a list, dict or string has this length
  exists      true: the path must exist; false: it must not
  gte, lte    a number is at least / at most this
  where       on a list: keep only elements whose fields equal these values,
              then apply "length", or "sum" (a field to add up) with
              "equals", "gte" or "lte"

Run it by hand against an exported state:
  GRADER_STATE=<folder holding service/export> GRADER_OUT=<folder> python3 verify.py
"""

import importlib.util
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
STATE_ROOT = os.environ.get("GRADER_STATE", "/grader/state")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
MISSING = object()


def load_checks():
    spec = importlib.util.spec_from_file_location("checks", os.path.join(HERE, "checks.py"))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.STATE, mod.CHECKS


def resolve(value, path):
    for seg in [s for s in str(path).split("/") if s]:
        if isinstance(value, list) and "=" in seg:
            key, want = seg.split("=", 1)
            value = next((v for v in value if isinstance(v, dict) and str(v.get(key)) == want), MISSING)
        elif isinstance(value, list) and seg.lstrip("-").isdigit():
            i = int(seg)
            value = value[i] if -len(value) <= i < len(value) else MISSING
        elif isinstance(value, dict):
            value = value.get(seg, MISSING)
        else:
            value = MISSING
        if value is MISSING:
            return MISSING
    return value


def same(a, b):
    """JSON equality that keeps 1 and True apart."""
    if isinstance(a, bool) or isinstance(b, bool):
        return type(a) is type(b) and a == b
    return a == b


def run_check(state, check):
    value = resolve(state, check["path"])
    if "exists" in check:
        return (value is not MISSING) == bool(check["exists"])
    if value is MISSING:
        return False
    if "where" in check:
        if not isinstance(value, list):
            return False
        value = [v for v in value if isinstance(v, dict) and all(same(v.get(k), w) for k, w in check["where"].items())]
        if "sum" in check:
            value = sum(v.get(check["sum"], 0) for v in value)
        elif "length" in check:
            return len(value) == check["length"]
    tests = {
        "equals": lambda v, w: same(v, w),
        "not_equals": lambda v, w: not same(v, w),
        "in": lambda v, w: any(same(v, x) for x in w),
        "contains": lambda v, w: (w in v) if isinstance(v, (str, list)) else False,
        "not_contains": lambda v, w: (w not in v) if isinstance(v, (str, list)) else False,
        "matches": lambda v, w: isinstance(v, str) and re.search(w, v) is not None,
        "length": lambda v, w: hasattr(v, "__len__") and len(v) == w,
        "gte": lambda v, w: isinstance(v, (int, float)) and not isinstance(v, bool) and v >= w,
        "lte": lambda v, w: isinstance(v, (int, float)) and not isinstance(v, bool) and v <= w,
    }
    used = [k for k in tests if k in check]
    if not used:
        raise ValueError(f"check {check.get('name')!r} has no test")
    return all(tests[k](value, check[k]) for k in used)


def main():
    state_rel, checks = load_checks()
    path = os.path.join(STATE_ROOT, state_rel)
    try:
        with open(path, encoding="utf-8") as f:
            state = json.load(f)
    except (OSError, ValueError) as e:
        print(f"state {path} unreadable: {e}; not grading", file=sys.stderr)
        return 2
    dims = {}
    for check in checks:
        ok = run_check(state, check)
        dims[check["name"]] = 1 if ok else 0
        print(("PASS " if ok else "FAIL ") + check["name"], file=sys.stderr)
    reward = 1 if checks and all(dims.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as f:
        json.dump({"reward": reward, "dimensions": dims}, f, sort_keys=True)
        f.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
