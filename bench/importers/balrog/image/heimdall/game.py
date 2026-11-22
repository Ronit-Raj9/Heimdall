"""One BALROG episode stepped one action at a time, as upstream's evaluator does."""

import sys

sys.path.insert(0, "/opt/heimdall")

import common  # noqa: E402


class Episode:
    def __init__(self, cfg, env_name, task, seed):
        self.env_name, self.task, self.seed = env_name, task, seed
        self.env, obs = common.make(cfg, env_name, task, seed)
        self.instruction = common.instruction_prompt(self.env, env_name, obs)
        self.max_steps = common.max_steps(self.env)
        self.observation = common.obs_text(obs)
        self.steps = 0
        self.done = False
        self.episode_return = 0.0

    def step(self, candidate):
        """Steps one action the way upstream's evaluator does.

        An action outside the language action space becomes the environment's
        default action, with upstream's feedback line in front of the
        observation. Returns (used_action, observation text, reward).
        """
        action = self.env.check_action_validity(candidate)
        obs, reward, terminated, truncated, _info = self.env.step(action)
        self.steps += 1
        self.episode_return += float(reward)
        text = common.obs_text(obs)
        if action != candidate:
            text = (f"Your previous output did not contain a valid action. Defaulted to action: {action}\n\n"
                    f"Observation:\n{text}")
        self.observation = text
        if terminated or truncated or self.steps >= self.max_steps:
            self.done = True
        return action, text, float(reward)

    def progression(self):
        return common.progression(self.env)
