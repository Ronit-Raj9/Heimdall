#!/usr/bin/env python3
"""Hidden verifier of acme-team/split-refactor (fictional, written for this example).

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
    sys.path.insert(0, WS)
    prices, stock = read("acme/prices.py"), read("acme/stock.py")
    out = [
        ("prices_no_legacy", "legacy_round" not in prices),
        ("stock_no_legacy", "legacy_round" not in stock),
        ("prices_use_round_money", "round_money(" in prices),
        ("stock_use_round_money", "round_money(" in stock),
    ]
    try:
        from acme import prices as p, stock as s
        out.append(("gross_price", p.gross(10) == 12.0))
        out.append(("bulk_rounds_half_up", p.bulk(1.005, 1) == 1.01))
        out.append(("stock_rounds_half_up", s.stock_value([(1.005, 1)]) == 1.01))
    except Exception as e:  # an import error fails the behaviour checks
        print("import failed:", e)
        out.append(("imports", False))
    return out


def main():
    dims = {name: (1 if ok else 0) for name, ok in checks()}
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(OUT, "reward.json"), "w") as f:
        json.dump({"reward": 1 if dims and all(dims.values()) else 0, "dimensions": dims}, f)
    print(json.dumps(dims))


if __name__ == "__main__":
    sys.exit(main())
