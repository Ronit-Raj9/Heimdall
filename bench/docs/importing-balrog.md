# Importing BALROG

`bifrost-bench import balrog` turns [BALROG](https://github.com/balrog-ai/BALROG)
(Benchmark for Agentic LLM and VLM Reasoning On Games, balrogai.com) into one
suite of track `mcp`. In each task the agent plays one episode of one game
through an MCP service and is scored by upstream's progression metric.

## What upstream is

Checked against commit `b7afe79e3e4265811cfa985ed7c95c4d1a11e3f5` (9 Apr
2026, package version 0.1.0). License: MIT.

Six environments, with the tasks and episode counts of upstream's evaluation
config (`balrog/config/config.yaml`):

| Env | Tasks | Episodes per task | Episodes | Progression |
|---|---|---|---|---|
| `babyai` | 5 (BabyAI-MixedTrainLocal goto, pickup, open, putnext, pick_up_seq_go_to) | 10 | 50 | 1 when the mission is completed |
| `babaisai` | 40 Baba Is AI levels | 3 | 120 | 1 when the level is won |
| `textworld` | 3 (treasure_hunter, the_cooking_game, coin_collector) | 10 | 30 | score / max score, 1 on a win |
| `crafter` | 1 (default) | 10 | 10 | achievements / 22 |
| `nle` | 1 (NetHackChallenge-v0) | 5 | 5 | upstream's NetHack progress (dungeon or experience level as a human-play percentile) |
| `minihack` | 8 (Boxoban, MazeWalk, Corridor, CorridorBattle, Quest) | 5 | 40 | 1 when the level is solved |

255 episodes in all. Upstream's agents see a text observation per step
(`long_term_context` and `short_term_context`), an instruction prompt per
environment and task, and answer with one action from the environment's
language action space; an invalid action becomes the default action with a
feedback line.

## What the importer makes

One task per episode, named `balrog-<env>-<task>-s<seed>` (for example
`balrog-babyai-mixedtrainlocal-v0-goto-s3`), tagged `balrog` and the env.

- **The service.** Every task starts one service, `balrog`, from the BALROG
  image the importer builds: an MCP server (streamable HTTP, behind the tool
  proxy) with two tools. `observe` returns the step count and limit, the
  current progress, the observation and the valid actions without advancing
  the game; `act {action}` takes exactly one step through upstream's
  `check_action_validity` and `step` and returns the next observation. Once
  the episode is over (terminated, truncated or the step limit reached),
  `act` refuses. After every step the service rewrites `/state/run.json`:
  configuration, every action (as sent and as applied), steps, done, return,
  upstream's `get_stats()` and progression. The bench exports it as
  `state/balrog/run`.
- **The instruction.** Upstream's own instruction prompt for the env and task
  (for BabyAI it carries the episode's mission), and how to play through the
  two tools.
- **The verifier** runs in the same image with no network. It checks the
  exported episode is the one the hidden `verifier/tests/task.json` names,
  replays the applied actions from the seed in a fresh upstream environment
  as a cross-check (dimension `replay_agrees`), and writes upstream's
  progression as the reward. The suite's `pass_threshold` is 1: a pass means
  the goal was reached; the reward keeps partial progress (TextWorld score,
  Crafter achievements, NetHack depth) for averaging.
- **Controls.** `controls/reference.sh` plays a solving action sequence
  through the MCP server with a raw JSON-RPC client. BabyAI's comes from
  Minigrid's BabyAI bot, TextWorld's from the game's own walkthrough
  (`policy_commands`); the catalog keeps a plan only when replaying it
  reaches progression 1. `controls/mutant1.sh` is the plan without its last
  action (written only when that fails). `controls/empty.sh` takes no action.
  `controls/exploit.sh` forges a winning state file in the workspace; it
  fails because the verifier reads only the exported state.
- **Drafts.** Baba Is AI, Crafter, NetHack and MiniHack ship no solver or
  walkthrough upstream, so their tasks get a reference control that refuses
  with the reason, and they stay drafts at admission. They are complete,
  runnable tasks: agents can be scored on them; only the reference check is
  missing. An episode whose environment does not build in the image is also
  written, as a draft whose reference prints the error.

## Usage

```sh
git clone https://github.com/balrog-ai/BALROG datasets/balrog
bifrost-bench import balrog -from datasets/balrog -suite balrog
bifrost-bench suite lint balrog
bifrost-bench suite lock balrog
bifrost-bench suite admit balrog
```

Settings (`-set key=value`):

| Key | Default | Meaning |
|---|---|---|
| `envs` | every env | Comma-separated subset of `babyai,babaisai,textworld,crafter,nle,minihack`. |
| `image` | built by the importer | A prebuilt BALROG image tag; skips the build. |

`-ids` takes bench task ids or upstream task names; `-limit` caps the count.

The importer needs Docker. It copies the clone's package (`setup.py`,
`README.md`, `LICENSE`, `balrog/`) into `image/upstream/`, builds
`bench/balrog:<version>-<hash>` (the build installs the games from the
package index and git, and downloads Boxoban levels and upstream's TextWorld
games, as `balrog-post-install` does: the only step with network), then runs
`catalog.py` in the image with no network to list episodes, instruction
prompts and reference plans.

## Differences from upstream

- **Seeds.** Upstream draws a fresh random seed per episode. Here episode
  `i` of a task uses seed `i`, and Python's and NumPy's generators are seeded
  before `make_env` as well as before `reset` (BabyAI-MixedTrainLocal picks
  its level with the global generator inside `make_env`), so every task is
  one reproducible episode.
- **TextWorld game choice.** Upstream's TextWorld factory cycles through the
  games of a task with a process-wide counter when the config seed is null.
  Here the seed is passed, so episode `i` plays game `i mod n` (n = 25 per
  task in upstream's game set): games 0 to 9 of each task, where upstream's
  first worker plays games 1 to 10.
- **The agent harness.** Upstream's agents (naive, chain of thought, few
  shot) keep a 16-message history and are prompted to output one action.
  Here the agent under test drives the game through MCP with its own
  context and prompting; upstream's instruction prompt is the task text.
  Scores are comparable across agents run here, not with upstream's
  leaderboard.
- **Images.** Upstream's VLM mode (image observations) is not exposed; the
  service is text only, as upstream's LLM leaderboard.

## Files

| Path | What |
|---|---|
| `importers/balrog/` | The importer: settings, catalog, task writing. |
| `importers/balrog/image/heimdall/server.py` | The MCP game service. |
| `importers/balrog/image/heimdall/game.py` | One episode stepped as upstream's evaluator does. |
| `importers/balrog/image/heimdall/verify.py` | The verifier. |
| `importers/balrog/image/heimdall/catalog.py` | The catalog the importer reads, with reference plans. |
| `importers/balrog/image/control.py` | The reference control's JSON-RPC client. |
