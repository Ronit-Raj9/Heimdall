"""Catalog of BALROG episodes for the Heimdall importer.

Usage: python3 -I /opt/heimdall/catalog.py [env ...]

Prints JSON on stdout: for every environment and task in upstream's
config.yaml and every episode seed 0..num_episodes-1, the instruction prompt
upstream gives the agent, the step limit, the action space, and, where one
can be computed here, a reference action sequence that reaches progression 1
(BabyAI: Minigrid's BabyAI bot; TextWorld: the game's own walkthrough). An
episode whose environment cannot be built is listed with the error.
"""

import json
import sys
import traceback

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402
from game import Episode  # noqa: E402


def babyai_reference(cfg, task, seed):
    from minigrid.utils.baby_ai_bot import BabyAIBot
    from balrog.environments.babyai_text.clean_lang_wrapper import BABYAI_ACTION_SPACE

    ep = Episode(cfg, "babyai", task, seed)
    bot = BabyAIBot(ep.env.unwrapped)
    plan = []
    while not ep.done and len(plan) < ep.max_steps:
        a = int(bot.replan())
        if a >= len(BABYAI_ACTION_SPACE):
            break  # the bot's "done": the mission is already complete
        text = BABYAI_ACTION_SPACE[a]
        plan.append(text)
        ep.step(text)
    return plan


def textworld_game_file(task, seed):
    """The game file upstream's TextWorldFactory picks for this seed."""
    import glob
    import os
    from pathlib import Path

    files = []
    for pattern in ["*.ulx", "*.z8"]:
        for entry in sorted(glob.glob(os.path.join(common.TW_GAMES, f"**/{pattern}"), recursive=True)):
            if Path(entry).parent.name == task:
                files.append(entry)
    return files[seed % len(files)]


def textworld_reference(cfg, task, seed):
    import textworld

    env = textworld.start(textworld_game_file(task, seed), request_infos=textworld.EnvInfos(policy_commands=True))
    state = env.reset()
    env.close()
    return list(state["policy_commands"])


REFERENCES = {"babyai": babyai_reference, "textworld": textworld_reference}


def run(cfg, env_name, task, seed, plan):
    ep = Episode(cfg, env_name, task, seed)
    for a in plan:
        if ep.done:
            break
        ep.step(a)
    return ep.progression()


def entry(cfg, env_name, task, seed):
    e = {"env": env_name, "task": task, "seed": seed}
    try:
        ep = Episode(cfg, env_name, task, seed)
    except Exception as ex:
        e["error"] = f"{type(ex).__name__}: {ex}"
        return e
    e["instruction"] = ep.instruction
    e["max_steps"] = ep.max_steps
    e["actions"] = common.action_list(ep.env)
    e["first_observation"] = ep.observation[:4000]
    fn = REFERENCES.get(env_name)
    if fn is None:
        e["reference_note"] = "upstream ships no solver or walkthrough for this environment"
        return e
    try:
        plan = fn(cfg, task, seed)
        reached = run(cfg, env_name, task, seed, plan)
        if reached < 1.0:
            e["reference_note"] = f"computed plan reaches progression {reached}, not 1"
            return e
        e["reference"] = plan
        if plan:
            e["mutant_fails"] = run(cfg, env_name, task, seed, plan[:-1]) < 1.0
    except Exception as ex:
        e["reference_note"] = f"no reference: {type(ex).__name__}: {ex}"
        traceback.print_exc(file=sys.stderr)
    return e


def main():
    real_stdout = sys.stdout
    sys.stdout = sys.stderr  # upstream environments print to stdout; keep it for the JSON
    cfg = common.load_config()
    wanted = sys.argv[1:] or common.ENVS
    out = {"format": "heimdall-balrog-catalog/1", "episodes": []}
    for env_name in wanted:
        try:
            tasks = common.tasks_of(cfg, env_name)
        except Exception as ex:
            out.setdefault("errors", []).append(f"{env_name}: {ex}")
            continue
        for task in tasks:
            for seed in range(common.episodes_of(cfg, env_name)):
                out["episodes"].append(entry(cfg, env_name, task, seed))
                sys.stderr.write(f"catalog: {env_name} {task} {seed}\n")
    json.dump(out, real_stdout)
    real_stdout.flush()


if __name__ == "__main__":
    main()
