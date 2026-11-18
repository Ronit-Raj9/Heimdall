#!/usr/bin/env python3
"""Hidden verifier of acme-team/review-patch (fictional, written for this example).

Reads the workspace with the team's diff applied (read-only at /workspace,
or GRADER_WORKSPACE) and writes /grader/out/reward.json (or GRADER_OUT):
{"reward": 1 or 0, "dimensions": {"<check>": 1 or 0}}. The reward is 1
only when every check passes. Standard library only, no network.
"""

import json
import os
import sys

WS = os.environ.get("GRADER_WORKSPACE", "/workspace")
OUT = os.environ.get("GRADER_OUT", "/grader/out")


def read(rel):
    try:
        with open(os.path.join(WS, rel)) as f:
            return f.read()
    except OSError:
        return ""


def checks():
    try:
        review = json.loads(read("review.json"))
    except ValueError:
        return [("review_json", False)]
    findings = review.get("findings") or []
    at14 = any(str(f.get("file", "")).endswith("acme/pricing.py") and abs(int(f.get("line", 0)) - 14) <= 1 for f in findings if isinstance(f, dict))
    return [
        ("review_json", True),
        ("rejects_patch", review.get("approve") is False),
        ("finds_skipped_first_item", at14),
    ]


def main():
    dims = {name: (1 if ok else 0) for name, ok in checks()}
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(OUT, "reward.json"), "w") as f:
        json.dump({"reward": 1 if dims and all(dims.values()) else 0, "dimensions": dims}, f)
    print(json.dumps(dims))


if __name__ == "__main__":
    sys.exit(main())
