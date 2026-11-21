#!/usr/bin/env python3
"""CI release gate over the Heimdall bench API.

Plan an experiment, run it, wait for it, and fail the job when the release
policy blocks. Or, with --exp, gate an experiment that already ran.

    python3 ci_gate.py experiments/smoke.yaml            # plan, run, wait, gate
    python3 ci_gate.py --exp e_pilot_mixed_nc93 --allow-recorded

Environment: HEIMDALL_URL (default http://127.0.0.1:8090) and
BENCH_WRITE_TOKEN (needed to plan and start; reads need no token).

The gate fails on the verdicts in --fail-on (default regressed,invalid, as
`bifrost-bench check -fail-on`) and on a release policy block; --policy-only
gates on the policy alone.

Exit codes: 0 gate passed, 1 verdict or policy fails the gate, 2 experiment did not finish,
3 server is not live (a recorded or synthetic answer is never a real result
unless --allow-recorded), 4 plan refused or request failed.
"""

import argparse
import os
import sys

try:
    import heimdall
except ImportError:  # running from the repository without installing heimdall-bench
    sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "python"))
    import heimdall

from heimdall import Client, HeimdallError


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description="Heimdall CI release gate")
    ap.add_argument("experiment", nargs="?", help="experiment YAML file to plan and run")
    ap.add_argument("--exp", help="gate an existing experiment id instead of running one")
    ap.add_argument("--url", default=os.environ.get("HEIMDALL_URL", "http://127.0.0.1:8090"))
    ap.add_argument("--token", default=os.environ.get("BENCH_WRITE_TOKEN"))
    ap.add_argument("--timeout", type=float, default=3600, help="seconds to wait for the experiment")
    ap.add_argument("--interval", type=float, default=10, help="seconds between status polls")
    ap.add_argument("--allow-recorded", action="store_true", help="accept a recorded or synthetic server (demos only)")
    ap.add_argument("--fail-on-warn", action="store_true", help="also fail when the policy warns")
    ap.add_argument("--fail-on", default=DEFAULT_FAIL_ON, help="comma-separated verdicts that fail the gate (default %s)" % DEFAULT_FAIL_ON)
    ap.add_argument("--policy-only", action="store_true", help="gate on the release policy alone, ignoring the verdict")
    args = ap.parse_args(argv)
    if bool(args.experiment) == bool(args.exp):
        ap.error("give an experiment file or --exp, not both")

    client = Client(args.url, token=args.token, require_live=not args.allow_recorded)
    try:
        mode = client.health()["mode"]
        print("server %s, mode %s" % (args.url, mode))
        exp_id = args.exp or run(client, args.experiment, args.timeout, args.interval)
        if exp_id is None:
            return 4
        status = client.experiment(exp_id)
        if status["state"] != "done":
            print("experiment %s ended %s: no release decision" % (exp_id, status["state"]))
            return 2
        result = client.check(exp_id)
    except heimdall.ModeError as err:
        print("refusing to gate: %s (pass --allow-recorded for a demo)" % err.message)
        return 3
    except heimdall.HeimdallTimeout as err:
        print("timed out: %s" % err)
        return 2
    except HeimdallError as err:
        print("request failed: %s" % err)
        return 4

    policy = result.policy_result
    print("verdict %s, action %s, policy %s (mode %s)" % (result.decision, result.action, result.policy_decision or "none", result.mode))
    for c in (policy or {}).get("checks", []):
        if c["status"] != "pass":
            print("  %s %s: %s %s" % (c["status"], c["id"], c.get("threshold", ""), c.get("reason", "")))
    reason = gate_failure(result, [] if args.policy_only else parse_fail_on(args.fail_on), args.fail_on_warn)
    if reason:
        print("gate FAILED: %s" % reason)
        return 1
    print("gate passed")
    return 0


DEFAULT_FAIL_ON = "regressed,invalid"


def parse_fail_on(value: str):
    """The verdicts of a --fail-on list."""
    return [v.strip() for v in value.split(",") if v.strip()]


def gate_failure(result, fail_on, fail_on_warn: bool = False):
    """Why the gate fails, or None. A verdict listed in fail_on fails it, as
    the direct CLI's default -fail-on does (QC2-22-03), and so does a policy
    block (or warn with fail_on_warn)."""
    if result.blocked or (fail_on_warn and result.policy_decision == "warn"):
        return "release policy %s" % result.policy_decision
    if result.decision in fail_on:
        return "verdict %s, action %s" % (result.decision, result.action)
    return None


def run(client: Client, path: str, timeout: float, interval: float):
    """Plan, create, start and wait; returns the experiment id or None."""
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    plan = client.plan(text)
    print("plan: %d slots, %d pairs, MDE %.1f pp" % (plan["slots"], plan["pairs"], plan["mde"]))
    for w in plan["warnings"]:
        print("  warning: %s" % w)
    if plan["errors"]:
        for e in plan["errors"]:
            print("  error: %s" % e)
        return None
    started = client.run_experiment(text)
    exp_id = started["exp_id"]
    print("started %s" % exp_id)

    def show(s):
        slots = s["slots"]
        print("  %s: %d/%d slots done, %d running, %d failed" % (s["state"], slots["done"], slots["total"], slots["running"], slots["failed"]))

    client.wait_for_experiment(exp_id, timeout=timeout, interval=interval, on_status=show)
    return exp_id


if __name__ == "__main__":
    sys.exit(main())
