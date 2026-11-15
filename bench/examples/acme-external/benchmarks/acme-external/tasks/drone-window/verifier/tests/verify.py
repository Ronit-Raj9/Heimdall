#!/usr/bin/env python3
"""Verifier of the drone-window task.

It runs after the agent exited, with no network, in a fresh container from
the suite's base image: a pristine copy of the workspace with the agent's
diff applied is at /workspace, this folder is /grader/tests, and the reward
goes to /grader/out/reward.json as
{"reward": 0 or 1, "dimensions": {"<check>": 0 or 1, ...}}.

The expected plan follows from the forecasts recorded in the task's cassette
for 09:00 on 2026-11-16: riverside gusts 22 km/h and 10 percent rain (fly),
hilltop gusts 41 km/h (hold: wind), quayside 70 percent rain (hold: rain).
"""

import json
import os

WORKSPACE = os.environ.get("GRADER_WORKSPACE") or ("/workspace" if os.path.isdir("/workspace") else os.getcwd())
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")


def as_set(v):
    return set(v) if isinstance(v, list) and all(isinstance(x, str) for x in v) else None


def main():
    try:
        with open(os.path.join(WORKSPACE, "plan.json")) as f:
            plan = json.load(f)
        if not isinstance(plan, dict):
            plan = {}
    except (OSError, ValueError):
        plan = {}
    fly, hold = as_set(plan.get("fly")), as_set(plan.get("hold"))
    checks = {
        "date": plan.get("date") == "2026-11-16",
        "riverside_flies": fly is not None and "riverside" in fly,
        "hilltop_holds_wind": hold is not None and "hilltop" in hold,
        "quayside_holds_rain": hold is not None and "quayside" in hold,
        "no_extra_sites": fly is not None and hold is not None and (fly | hold) == {"riverside", "hilltop", "quayside"} and not (fly & hold),
    }
    reward = 1 if all(checks.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w") as f:
        json.dump({"reward": reward, "dimensions": {k: int(v) for k, v in checks.items()}}, f)
    print(json.dumps({"reward": reward, "checks": checks}))


if __name__ == "__main__":
    main()
