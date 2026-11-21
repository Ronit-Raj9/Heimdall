"""Grade one EnterpriseOps-Gym task offline (bifrost-bench import enterpriseops).

Each domain service exported the results of the task's SQL checks
(/grader/state/<service>/checks, the stdout of `bench_front.py export`). The
checks and their expected values are in /grader/tests/checks.json, by
service. A check passes as in upstream benchmark/verifier.py
(_extract_value_from_sql_result, _compare_values); the task passes when every
check passes (upstream overall_success). Writes /grader/out/reward.json with
the verifier-level pass rate as a dimension. A missing export writes no
reward, so the grade is undecided.
"""

import json
import os
import sys

TESTS = "/grader/tests"
STATE = "/grader/state"
OUT = "/grader/out"


def extract(result):
    if not result or not result.get("success"):
        return None
    d = result.get("result", {})
    if isinstance(d, dict):
        for key in ("data", "rows"):
            if key in d:
                rows = d[key]
                if isinstance(rows, list) and rows:
                    if len(rows) == 1 and isinstance(rows[0], dict) and len(rows[0]) == 1:
                        return list(rows[0].values())[0]
                    if len(rows) == 1 and isinstance(rows[0], list) and len(rows[0]) == 1:
                        return rows[0][0]
                    if len(rows) == 1:
                        return rows[0]
                return rows
        if "content" in d:
            c = d["content"]
            if isinstance(c, list):
                for item in c:
                    if isinstance(item, dict) and item.get("type") == "text":
                        return item.get("text", d)
            return c
        if "result" in d:
            return d["result"]
    return d


def compare(actual, expected, kind):
    try:
        if kind == "equals":
            return actual == expected
        if kind == "greater_than":
            return actual > expected
        if kind == "less_than":
            return actual < expected
        if kind == "contains":
            return expected in str(actual)
    except Exception:  # noqa: BLE001
        return False
    return False


def main():
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(TESTS, "checks.json")) as f:
        checks = json.load(f)
    details, passed, total = [], 0, 0
    for service, items in sorted(checks.items()):
        path = os.path.join(STATE, service, "checks")
        if not os.path.exists(path):
            print("no exported checks for service %s; grade undecided" % service, file=sys.stderr)
            return 0
        with open(path) as f:
            results = json.load(f)
        if len(results) != len(items):
            print("service %s exported %d results for %d checks" % (service, len(results), len(items)), file=sys.stderr)
            return 0
        for check, res in zip(items, results):
            cfg = check.get("validation_config", {})
            actual = extract(res)
            ok = res.get("success", False) and compare(actual, cfg.get("expected_value"), cfg.get("comparison_type", "equals"))
            total += 1
            passed += 1 if ok else 0
            details.append({"service": service, "name": check.get("name"), "passed": bool(ok),
                            "expected": cfg.get("expected_value"), "actual": actual, "error": res.get("error")})
    reward = 1 if total and passed == total else 0
    with open(os.path.join(OUT, "reward.json"), "w") as f:
        json.dump({"reward": reward, "dimensions": {"verifier_pass_rate": (passed / total) if total else 0, "checks_passed": passed, "checks": total}}, f)
    with open(os.path.join(OUT, "checks.json"), "w") as f:
        json.dump(details, f, indent=1, default=str)
    print("checks passed %d/%d reward %d" % (passed, total, reward))
    return 0


if __name__ == "__main__":
    sys.exit(main())
