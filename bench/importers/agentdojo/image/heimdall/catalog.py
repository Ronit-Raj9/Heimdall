"""Heimdall AgentDojo catalog: prints one suite's tasks as JSON for the importer.

The importer runs this inside the image with no network, so upstream code
never runs on the host. Usage:

  python3 -I /opt/heimdall/catalog.py <benchmark version> <suite>

Output: the package and benchmark versions, upstream's default system
message, the suite's tools, its injection vectors, every user task (prompt,
difficulty, ground-truth calls and output, the injection vectors its ground
truth reads, whether upstream's utility passes with no action at all, and
whether it fails on the near miss of the ground truth without its last call),
every injection task (goal, difficulty) and the attacks a Heimdall condition
can plant.
"""

import json
import sys

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402
from agentdojo.agent_pipeline.agent_pipeline import load_system_message  # noqa: E402
from agentdojo.attacks.base_attacks import FixedJailbreakAttack  # noqa: E402
from agentdojo.functions_runtime import FunctionsRuntime  # noqa: E402


def calls(function_calls):
    return [{"function": c.function, "args": json.loads(json.dumps(dict(c.args), default=str))} for c in function_calls]


def near_miss_passes(suite, task, start, calls_):
    """Whether utility passes after the given calls and no answer.

    The near miss is the ground truth without its last call and without the
    answer; the importer makes it a mutant control only when it fails.
    """
    pre = start.model_copy(deep=True)
    post = start.model_copy(deep=True)
    runtime = FunctionsRuntime(suite.tools)
    for c in calls_:
        runtime.run_function(post, c.function, c.args)
    return common.check_utility(task, "", pre, post, list(calls_))


def main():
    if len(sys.argv) != 3:
        sys.stderr.write("usage: catalog.py <benchmark version> <suite>\n")
        sys.exit(2)
    version, name = sys.argv[1], sys.argv[2]
    suite = common.load_suite(version, name)
    probe = FixedJailbreakAttack("{goal}", suite, common.StubPipeline())
    env = suite.load_and_inject_default_environment({})
    vectors = suite.get_injection_vector_defaults()
    user_tasks = []
    for tid, task in sorted(suite.user_tasks.items(), key=lambda kv: int(kv[0].rsplit("_", 1)[1])):
        try:
            candidates = probe.get_injection_candidates(task)
        except ValueError:
            candidates = []
        start = task.init_environment(env.model_copy(deep=True))
        empty_passes = common.check_utility(task, "", start, start.model_copy(deep=True), [])
        gt = task.ground_truth(task.init_environment(env.model_copy(deep=True)))
        mutant_fails = bool(gt) and not near_miss_passes(suite, task, start, gt[:-1])
        user_tasks.append({
            "id": tid,
            "prompt": task.PROMPT,
            "difficulty": task.DIFFICULTY.name.lower(),
            "ground_truth": calls(gt),
            "ground_truth_output": task.GROUND_TRUTH_OUTPUT,
            "injection_vectors": sorted(candidates),
            "empty_passes": empty_passes,
            "mutant_fails": mutant_fails,
        })
    injection_tasks = [
        {"id": tid, "goal": task.GOAL, "difficulty": task.DIFFICULTY.name.lower()}
        for tid, task in sorted(suite.injection_tasks.items(), key=lambda kv: int(kv[0].rsplit("_", 1)[1]))
    ]
    out = {
        "agentdojo_version": common.package_version(),
        "benchmark_version": version,
        "benchmark_versions": common.benchmark_versions(),
        "suite": name,
        "system_message": load_system_message(None),
        "tools": [{"name": f.name, "description": f.description} for f in suite.tools],
        "injection_vectors": sorted(vectors),
        "user_tasks": user_tasks,
        "injection_tasks": injection_tasks,
        "attacks": [a for a in common.SUPPORTED_ATTACKS if a in common.ATTACKS or a == "adaptive"],
    }
    json.dump(out, sys.stdout, ensure_ascii=False, indent=1)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
