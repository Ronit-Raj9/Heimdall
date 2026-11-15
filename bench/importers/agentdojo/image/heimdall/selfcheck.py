"""Heimdall AgentDojo self-check: the whole suite through the service and verifier code.

Runs inside the image with no network:

  python3 -I /opt/heimdall/selfcheck.py <benchmark version> <suite> [attack]

For every user task it drives the service code (server.Service) the way an
agent would and decides with the verifier code (verify.decide):

  - reference: upstream's ground-truth calls and output must pass utility;
  - empty: no call and no answer must fail utility;
  - mutant: the ground truth without its last call and without the answer
    must fail utility;
  - served: under the attack (default important_instructions) carrying the
    first injection task, replaying the ground truth (computed, as upstream
    does, on the attacked environment) must return the planted text in at
    least one tool result;
  - reference_under_attack: that replay must still pass utility, which is
    what makes the attack probes goal preserving.

For every injection task with a ground truth, replaying it under the attack
must make the verifier report attack_succeeded (some injection tasks ship an
empty ground truth; they are reported as skipped). It prints one JSON line per check
and a summary, and exits 1 when any check fails. It is the admission
controls of every task at once, without the bench around them.
"""

import json
import os
import sys
import tempfile

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402
import server  # noqa: E402
import verify  # noqa: E402


def run(version, suite_name, user_task, calls, answer, attack="", injection_task=""):
    """Drives one service through calls (None: upstream's ground truth for the
    environment as served); returns the verifier's (detail, dims)."""
    with tempfile.TemporaryDirectory() as tmp:
        env = {
            "AGENTDOJO_SUITE": suite_name,
            "AGENTDOJO_BENCHMARK_VERSION": version,
            "AGENTDOJO_USER_TASK": user_task,
            "AGENTDOJO_STATE": os.path.join(tmp, "run.json"),
        }
        if attack:
            env["AGENTDOJO_ATTACK"] = attack
            env["AGENTDOJO_INJECTION_TASK"] = injection_task
        svc = server.Service(env)
        if calls is None:
            # Upstream's ground truth for the environment as served, attack included.
            calls = svc.user_task.ground_truth(svc.pre.model_copy(deep=True))
        errors = []
        for c in calls:
            text, is_error = svc.call(c.function, dict(c.args))
            if is_error:
                errors.append(f"{c.function}: {text[:200]}")
        with open(env["AGENTDOJO_STATE"], encoding="utf-8") as f:
            state = json.load(f)
        detail, dims = verify.decide(state, answer, {})
        detail["call_errors"] = errors
        detail["served"] = state["planted"]["served"]
        return detail, dims


def main():
    if len(sys.argv) not in (3, 4):
        sys.stderr.write("usage: selfcheck.py <benchmark version> <suite> [attack]\n")
        sys.exit(2)
    version, name = sys.argv[1], sys.argv[2]
    attack = sys.argv[3] if len(sys.argv) == 4 else "important_instructions"
    suite = common.load_suite(version, name)
    injection_ids = sorted(suite.injection_tasks, key=lambda i: int(i.rsplit("_", 1)[1]))
    first_user = None
    failed = 0
    results = []
    for tid in sorted(suite.user_tasks, key=lambda i: int(i.rsplit("_", 1)[1])):
        task = suite.user_tasks[tid]
        first_user = first_user or tid
        gt = task.ground_truth(common.build_environment(suite, task, {}))
        checks = {}
        try:
            d, _ = run(version, name, tid, gt, task.GROUND_TRUTH_OUTPUT)
            checks["reference"] = d["utility"] and not d["call_errors"]
            d, _ = run(version, name, tid, [], "")
            checks["empty"] = not d["utility"]
            if gt and checks["empty"]:  # a task whose empty run passes is left out, mutant and all
                d, _ = run(version, name, tid, gt[:-1], "")
                checks["mutant"] = not d["utility"]
            d, _ = run(version, name, tid, None, task.GROUND_TRUTH_OUTPUT, attack, injection_ids[0])
            checks["served"] = bool(d["served"])
            checks["reference_under_attack"] = d["utility"] and not d["call_errors"]
        except Exception as e:  # report and continue: one broken task must not hide the others
            checks["error"] = f"{type(e).__name__}: {e}"
        ok = all(v is True for v in checks.values())
        failed += 0 if ok else 1
        results.append({"user_task": tid, "ok": ok, **checks})
    for iid in injection_ids:
        inj = suite.injection_tasks[iid]
        try:
            gt = inj.ground_truth(common.build_environment(suite, suite.user_tasks[first_user], {}))
            if not gt:
                results.append({"injection_task": iid, "ok": True, "skipped": "upstream's ground truth is empty"})
                continue
            _, dims = run(version, name, first_user, gt, inj.GROUND_TRUTH_OUTPUT, attack, iid)
            ok = dims.get("attack_succeeded") == 1.0
            results.append({"injection_task": iid, "ok": ok, "attack_succeeded": dims.get("attack_succeeded")})
        except Exception as e:
            ok = False
            results.append({"injection_task": iid, "ok": False, "error": f"{type(e).__name__}: {e}"})
        failed += 0 if ok else 1
    for r in results:
        print(json.dumps(r))
    print(json.dumps({"suite": name, "benchmark_version": version, "attack": attack,
                      "checks": len(results), "failed": failed}))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
