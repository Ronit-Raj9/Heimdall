"""Heimdall BALROG verifier: upstream's progression from the exported episode.

Runs after the agent is gone, with no network. Reads the service's exported
state (/grader/state/<service>/run, or BALROG_STATE_EXPORT), checks it is the
episode the hidden task file (/grader/tests/task.json) names, then replays
the applied actions from the seed in a fresh upstream environment as a
cross-check. The reward is upstream's progression metric in [0, 1]
(BabyAI, Baba Is AI: 1 on success; TextWorld: score / max score, 1 on a win;
Crafter: achievements / 22; NetHack and MiniHack: upstream's progress).

Writes /grader/out/reward.json and /grader/out/balrog.json. Exits 3, writing
no reward, when it cannot decide.
"""

import glob
import json
import os
import sys

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402

OUT = os.environ.get("BALROG_OUT", "/grader/out")


class Undecided(Exception):
    pass


def fail(reason):
    sys.stderr.write("balrog verifier: cannot decide: " + reason + "\n")
    try:
        with open(os.path.join(OUT, "balrog.json"), "w", encoding="utf-8") as f:
            json.dump({"decided": False, "reason": reason}, f)
    except OSError:
        pass
    sys.exit(3)


def find_state():
    explicit = os.environ.get("BALROG_STATE_EXPORT")
    if explicit:
        return explicit
    found = sorted(glob.glob("/grader/state/*/run"))
    if len(found) != 1:
        raise Undecided(f"expected one exported state file at /grader/state/*/run, found {len(found)}")
    return found[0]


def read_expected():
    path = os.environ.get("BALROG_TASK_FILE", "/grader/tests/task.json")
    if not os.path.isfile(path):
        return {}
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError) as e:
        raise Undecided(f"read {path}: {e}")


def replay(cfg, state):
    """Replays the applied actions; returns the replayed progression or None on error."""
    from game import Episode

    c = state["config"]
    try:
        ep = Episode(cfg, c["env"], c["task"], int(c["seed"]))
        for a in state.get("actions") or []:
            if ep.done:
                break
            ep.step(a["used"])
        return ep.progression()
    except Exception as e:  # upstream environments raise many kinds
        sys.stderr.write(f"balrog verifier: replay failed: {type(e).__name__}: {e}\n")
        return None


def decide(state, expected, cfg=None):
    if state.get("format") != common.STATE_FORMAT:
        raise Undecided(f"state format {state.get('format')!r} is not {common.STATE_FORMAT}")
    c = state.get("config") or {}
    for key in ("env", "task", "seed"):
        if expected and expected.get(key) != c.get(key):
            raise Undecided(f"service ran {key} {c.get(key)!r}, the task expects {expected.get(key)!r}")
    recorded = float(state.get("progression") or 0.0)
    if not 0.0 <= recorded <= 1.0:
        raise Undecided(f"recorded progression {recorded} is outside [0, 1]")
    replayed = replay(cfg or common.load_config(), state)
    agrees = replayed is not None and abs(replayed - recorded) < 1e-9
    detail = {
        "decided": True,
        "env": c["env"], "task": c["task"], "seed": c["seed"],
        "steps": state.get("steps"), "max_steps": state.get("max_steps"), "done": state.get("done"),
        "episode_return": state.get("episode_return"),
        "progression": recorded, "replayed_progression": replayed, "replay_agrees": agrees,
        "stats": state.get("stats"),
    }
    dims = {"progression": recorded, "replay_agrees": 1.0 if agrees else 0.0,
            "steps": float(state.get("steps") or 0)}
    return detail, dims


def main():
    real_stdout = sys.stdout
    sys.stdout = sys.stderr  # upstream environments print to stdout
    try:
        path = find_state()
        try:
            with open(path, encoding="utf-8") as f:
                state = json.load(f)
        except (OSError, ValueError) as e:
            raise Undecided(f"read {path}: {e}")
        detail, dims = decide(state, read_expected())
    except Undecided as e:
        fail(str(e))
    with open(os.path.join(OUT, "balrog.json"), "w", encoding="utf-8") as f:
        json.dump(detail, f, indent=2)
    with open(os.path.join(OUT, "reward.json"), "w", encoding="utf-8") as f:
        json.dump({"reward": dims["progression"], "dimensions": dims}, f)
    real_stdout.write(json.dumps(detail) + "\n")


if __name__ == "__main__":
    main()
