#!/usr/bin/env python3
"""Verifier of the invoice-fx task.

It runs after the agent exited, with no network, in a fresh container from
the suite's base image: a pristine copy of the workspace with the agent's
diff applied is the working directory, this folder is /grader/tests, and the
reward goes to /grader/out/reward.json as
{"reward": 0 or 1, "dimensions": {"<check>": 0 or 1, ...}}.

The expected numbers follow from the rates recorded in the task's cassette
(EUR 1 = USD 1.0842 = JPY 163.27 on 2026-11-13).
"""

import json
import os

WORKSPACE = os.environ.get("GRADER_WORKSPACE") or ("/workspace" if os.path.isdir("/workspace") else os.getcwd())
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
EXPECTED = {"invoice": "INV-2041", "eur": 1250.00, "usd": 1355.25, "jpy": 204088, "rate_date": "2026-11-13"}


def checks(quote):
    return {
        "invoice": quote.get("invoice") == EXPECTED["invoice"],
        "eur": isinstance(quote.get("eur"), (int, float)) and abs(quote["eur"] - EXPECTED["eur"]) < 0.005,
        "usd": isinstance(quote.get("usd"), (int, float)) and abs(quote["usd"] - EXPECTED["usd"]) <= 0.01,
        "jpy": isinstance(quote.get("jpy"), (int, float)) and abs(quote["jpy"] - EXPECTED["jpy"]) <= 1,
        "rate_date": quote.get("rate_date") == EXPECTED["rate_date"],
    }


def main():
    try:
        with open(os.path.join(WORKSPACE, "quote.json")) as f:
            quote = json.load(f)
        if not isinstance(quote, dict):
            quote = {}
    except (OSError, ValueError):
        quote = {}
    result = checks(quote)
    reward = 1 if all(result.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w") as f:
        json.dump({"reward": reward, "dimensions": {k: int(v) for k, v in result.items()}}, f)
    print(json.dumps({"reward": reward, "checks": result}))


if __name__ == "__main__":
    main()
