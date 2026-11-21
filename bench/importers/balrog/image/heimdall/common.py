"""Shared code of the Heimdall BALROG image: the service, verifier and catalog.

Environments, tasks, observations, action spaces and the progression metric
are upstream's own (balrog.environments.make_env with upstream's
config.yaml). This module only fixes the seed handling so that one bench task
is one reproducible episode, and renders observations as text.
"""

import os
import random

import numpy as np
from omegaconf import OmegaConf

STATE_FORMAT = "heimdall-balrog/1"
CONFIG_PATH = os.environ.get("BALROG_CONFIG", "")
TW_GAMES = os.environ.get("BALROG_TW_GAMES", "/opt/balrog-data/tw_games")

# Env names in the order of upstream's config (envs.names), with the config
# key that lists their tasks and the number of episodes upstream runs.
ENVS = ["babyai", "babaisai", "textworld", "crafter", "nle", "minihack"]


def load_config():
    import importlib.resources

    path = CONFIG_PATH or str(importlib.resources.files("balrog") / "config" / "config.yaml")
    cfg = OmegaConf.load(path)
    cfg.envs.textworld_kwargs.textworld_games_path = TW_GAMES
    # No recordings or save directories: the service writes its own state.
    for k in ("nle_kwargs", "minihack_kwargs"):
        cfg.envs[k].savedir = None
    return cfg


def tasks_of(cfg, env_name):
    return list(cfg.tasks[f"{env_name}_tasks"])


def episodes_of(cfg, env_name):
    return int(cfg.eval.num_episodes[env_name])


def seed_everything(seed):
    random.seed(seed)
    np.random.seed(seed)


def make(cfg, env_name, task, seed):
    """Upstream's environment for one episode, reset with the seed.

    Upstream seeds Python's and NumPy's generators after make_env and before
    reset; here they are seeded before both, since make_env itself draws
    (BabyAI-MixedTrainLocal picks its level with the global generator).
    For TextWorld the seed also picks the game, see below.
    Returns (env, first observation dict).
    """
    from balrog.environments import make_env

    if env_name == "textworld":
        # Upstream's TextWorld factory picks the game by envs.env_kwargs.seed
        # (game seed mod the number of games) and, with seed null, cycles a
        # process-wide counter; a fixed game per episode needs the seed.
        cfg = cfg.copy()
        cfg.envs.env_kwargs.seed = seed
    seed_everything(seed)
    env = make_env(env_name, task, cfg)
    seed_everything(seed)
    obs, _info = env.reset(seed=seed)
    return env, obs


def obs_text(obs):
    """The observation as BALROG's agents see it: long and short term context."""
    text = obs.get("text") or {}
    long_term = (text.get("long_term_context") or "").strip()
    short_term = (text.get("short_term_context") or "").strip()
    return "\n\n".join(p for p in (long_term, short_term) if p)


def instruction_prompt(env, env_name, obs):
    mission = obs.get("mission") if env_name == "babyai" else None
    return env.get_instruction_prompt(instructions=mission)


def action_list(env):
    space = env.env.language_action_space  # what upstream checks actions against
    try:
        return list(space)
    except TypeError:
        return []  # TextWorld accepts any command


def max_steps(env):
    return int(env.max_steps)


def progression(env):
    return float(env.get_stats().get("progression", 0.0))
