"""Grade one CL-Bench stream from the outcomes the clenv service exported.

Written by `bifrost-bench import clbench`. Runs offline in the task image.
Reward = sum of upstream instance rewards / (instances in the schedule x
upstream r_max), clipped to 0..1. Instances never reached count as zero.
With no exported result no reward is written, so the grade is undecided.
"""

import json
import math
import os
import sys

SRC = "/grader/state/clenv/result"
OUT = "/grader/out"

os.makedirs(OUT, exist_ok=True)
try:
    with open(SRC) as fh:
        doc = json.load(fh)
    total = int(doc["instances_total"])
    r_max = float(doc["r_max"])
    rows = doc["outcomes"]
    rewards = [float(r["reward"]) for r in rows if math.isfinite(float(r["reward"]))]
except Exception as exc:  # noqa: BLE001
    print(f"no usable exported result at {SRC}: {exc}", file=sys.stderr)
    sys.exit(0)

reward_sum = sum(rewards)
norm = 0.0
if total > 0 and r_max > 0:
    norm = max(0.0, min(1.0, reward_sum / (total * r_max)))
completed = int(doc.get("instances_completed", len(rewards)))
dims = {
    "normalized_reward": round(norm, 6),
    "completion": round(min(1.0, completed / total), 6) if total else 0.0,
    "mean_instance_reward": round(reward_sum / total, 6) if total else 0.0,
    "sessions_used": float(len(set(doc.get("sessions_started") or []))),
    "invalid_submissions": float(doc.get("invalid_submissions", 0)),
}
with open(os.path.join(OUT, "reward.json"), "w") as fh:
    json.dump({"reward": round(norm, 6), "dimensions": dims}, fh)
print(json.dumps({"family": doc.get("family"), "schedule": doc.get("schedule"), "reward": round(norm, 6), **dims}))
