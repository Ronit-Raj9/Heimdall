# Evaluations: benchmark your agent

Heimdall benchmarks **agents**, not models. You register your own agent once, pick many benchmarks at once, and get one **report card** per agent version and model. If you compare two setups (two versions of your agent, two models, or your agent against a baseline agent), you also get an A/B result per benchmark and overall.

The object that does this is an **evaluation**. In the web app it is the "Evaluate your agent" button on Home, which opens the wizard at `/evaluate`. The list of evaluations is at `/evaluations`, and each one has a live page at `/evaluations/<id>` that turns into its report card when the runs finish.

Every model call an agent makes goes through the [Bifrost](https://github.com/maximhq/bifrost) gateway, so any model Bifrost can reach can be used. See [models-and-providers.md](models-and-providers.md).

## What an evaluation is

An evaluation is one request: "run this agent (or these two setups) on these benchmarks, with this model, this many times, under these faults". Heimdall turns it into **one normal comparison per benchmark** (technical: one experiment per suite) and launches each through the usual experiment launch path, on your machine or on hosted workers. Each of those comparisons carries the evaluation's id, so its runs, evidence and verdict are the same as for any comparison you start by hand.

What you choose:

| Field | Meaning | Default |
|---|---|---|
| `name` | A label for you. | none |
| `mode` | What you want to learn: `score` (one setup, how good is it), `versions` (v1 against v2 of your agent), `models` (the same agent on two models), `opponent` (your agent against a baseline agent). | `score` |
| `arms` | The setups. One arm for `score`, two for the A/B modes. Each arm has a `label` (A, B), an `agent`, an optional `version`, a `model` and an optional `provider`. | none |
| `benchmarks` | Benchmark ids to run. You can give a `pack` instead, or both (the pack's benchmarks are added). | none |
| `pack` | A ready-made group of benchmarks for one skill (see below). | none |
| `tasks_per_benchmark` | How many tasks to take from each benchmark. Only ready (admitted) tasks are used, chosen to be different from each other. At least 6 are needed before any A/B gap can be called real; more tasks detect smaller changes (see Presets). | 10 (Quick look) |
| `trials` | How many times each task is repeated per arm. More tasks narrow the margin faster than more trials. | 1 |
| `faults` | Fault conditions to add. Normal conditions (`clean`) are always included. | `["clean"]` |
| `budget_usd` | A spending cap for the whole evaluation, split evenly over its benchmarks. The wizard fills in a budget that covers the expected cost (see Budget). | $5 per benchmark when not set |
| `concurrency` | Runs of one benchmark that go at once, 1 to 16. Your organization's worker slots still bound what runs side by side. | 4 |
| `preset` | The size preset the wizard started from (`quick`, `standard`, `thorough`); the numbers above are what runs. | none |

Status of an evaluation is one of `queued`, `running`, `done`, `failed` or `cancelled`. While it runs, its status is worked out live from its children (one child per benchmark, each with its own comparison id and status). A benchmark that could not run is still listed, with a `skipped` reason, so you always see what was left out and why.

Evaluations are stored as one JSON file each under the project folder, `.heimdall/evaluations/<id>.json`, through the same project store as everything else. This works the same locally and on the hosted platform, where each evaluation belongs to your organization.

## Packs

A pack is a named group of benchmarks that test one kind of skill. Picking a pack is the quick way to say "test my agent as a coding agent". Only benchmarks that exist in the library or in your organization appear in a pack.

| Pack | Name | Benchmarks |
|---|---|---|
| `coding` | Coding agent | `hd-harbor-tb2`, `hd-swe-verified`, `hd-swe-rebench`, `hd-swe-ib-lcb` |
| `terminal` | Terminal agent | `hd-harbor-tb2`, `hd-harbor-skills` |
| `support` | Support and tool use | `tau2`, `mcpmark`, `agentdojo-*` |
| `memory` | Memory | `longmemeval`, `locomo` |
| `research` | Research | `gaia`, `browsecomp-plus-*` |
| `voice` | Voice | `voicebench` |
| `safety` | Safety | `agentdojo-*`, `hd-swe-ib-swe` |
| `all-compatible` | Everything my agent can run | every benchmark your agent is compatible with |

A name ending in `*` means every benchmark whose id starts with that text. The live list is `GET /api/bench/evaluation-packs`.

## Capabilities and compatibility

Not every agent can run every benchmark. A chat agent behind an HTTP endpoint has no shell, so it cannot do a terminal benchmark. Heimdall checks this before anything runs, using **capabilities**: short words for what an agent can do and what a benchmark needs.

| Capability | The agent can |
|---|---|
| `shell` | run commands in a workspace |
| `tools` | call tools (function calls) |
| `mcp` | use MCP servers |
| `sessions` | keep going across several sessions (needed for memory tests) |
| `browser` | drive a web browser |
| `desktop` | use a desktop (computer use) |
| `voice` | take and produce speech |
| `gpu` | use a GPU |

**What your agent has.** You can list capabilities in the agent config (`capabilities:` in `agents/<id>.yaml`). If you leave it empty, Heimdall uses the default for the adapter:

| Adapter | Default capabilities |
|---|---|
| CLI coding agents (codex, claude-code, opencode, gemini-cli, mini-swe-agent and similar) | shell, tools, mcp, sessions (memory benchmarks run them across sessions through the notes and memory folder) |
| `remote_http`, `a2a`, `openai_agent` | tools, sessions |
| voice adapters | voice, sessions |
| computer use | desktop, browser, shell |
| custom-python, command | shell, tools |

**What a benchmark needs.** Worked out from its track and environment, unless the benchmark's `suite.yaml` has a `requires:` list, which wins:

| Track or environment | Needs |
|---|---|
| terminal, coding, docker workspace | shell |
| memory | sessions |
| support, tools, mcp | tools |
| research, web | tools (and browser if the environment says browser) |
| computer use, desktop | desktop |
| browser | browser |
| voice | voice |
| gpu | gpu |

**The check.** `GET /api/bench/agents/<id>/compatibility` lists every benchmark with `ok`, what it `requires`, what is `missing` and a `reason`. The wizard greys out incompatible benchmarks and shows the reason. When an evaluation is created, incompatible benchmarks are dropped and listed as skipped. If you build a comparison by hand with an agent that lacks a need, the planner refuses it with:

```
agent <id> cannot run benchmark <id>: needs <cap>
```

## Versions

Every time you save an agent's config (in the app, through `PUT /api/bench/agents/<id>/config`, when you create one from a template, or when you edit `agents/<id>.yaml` through the API), Heimdall saves a numbered snapshot: `v1`, `v2` and so on. A save that changes nothing (same config fingerprint as the latest version) does not make a new version. A version is the agent without its model and provider: an evaluation sets those per arm, so a change of the default model alone is no new version, and every run of a version on any model counts toward that version. An evaluation arm with no `version` is recorded at launch as the version that was latest then (`latest_at_launch: true`), and the report card and leaderboard show that exact version. Snapshots live in `.heimdall/agent-versions/<id>/v<N>.yaml` with an index file `versions.json`.

Why it matters: a report card is always for one exact version, so a score never silently changes meaning when someone edits the agent later. To compare two versions, pick mode `versions` and set `version` on each arm. In a comparison file you can write `agent: agents/<id>.yaml@v<N>` and the planner uses that frozen snapshot. Built-in library agents are versioned the same way (read-only, `v1`).

| Call | What it does |
|---|---|
| `GET /api/bench/agents/<id>/versions` | every version with its fingerprint, time, author, note and number of runs |
| `GET /api/bench/agents/<id>/versions/<n>` | one version's YAML |
| `POST /api/bench/agents/<id>/versions` with `{"note": "..."}` | save the current config as a new version now |

## Faults

Normal conditions are always run. You can add fault conditions to see how your agent copes when things go wrong. Each fault becomes a condition in every comparison of the evaluation.

| Fault | What happens | Where it applies |
|---|---|---|
| `clean` | The tasks as written, nothing added. | always |
| `model-down` | The model provider goes down for part of the run (the fault proxy answers with an outage). | any benchmark |
| `broken-tool` | Some tool calls return errors (tool proxy or hook fault injection). | any benchmark where the agent calls tools |
| `misleading-instruction` | The task comes with misleading or stale notes. | only benchmarks that have such a stress test; others skip it with a reason |

Faults cost runs: each fault adds as many runs as the normal condition.

## How many runs, and the estimate

Runs per benchmark = arms x tasks x trials x conditions, where tasks is the smaller of `tasks_per_benchmark` and the benchmark's ready tasks. For example, 2 arms, 8 tasks, 2 trials, clean plus `model-down` is 2 x 8 x 2 x 2 = 64 runs per benchmark.

The estimate forecasts exactly the tasks the launch will pick (the same spread over the ready tasks). When there is no sealed history for a setup or the benchmark, the time is "not known" rather than zero (see First estimates).

In `score` mode there is exactly one arm, and nothing else runs: 1 arm, 8 tasks, 2 trials, clean plus `model-down` is 1 x 8 x 2 x 2 = 32 runs per benchmark. The estimate counts the same runs the launch plans, so the cost and time you approve are the ones you pay for. (Evaluations created before one-arm runs existed also ran a hidden repeat of the arm; the report card ignores it and the leaderboards leave it out.) One exception adds runs, visibly: a score evaluation with `"stream": {"memory": "carry"}` also runs `<agent> (no memory)`, the same agent on the same tasks in the same order with empty memory, as the control for the gain from memory. It doubles the runs, the estimate counts it, and it is never on a leaderboard. Stream `replay: k` adds k runs at the end of every chain, also counted.

Before you launch, `POST /api/bench/evaluations/estimate` (same body as creating one) returns the number of runs, a cost range and a time range, overall and per benchmark. The cost comes from the pinned price list. If any model in the evaluation has no known price, the answer says `"known": false` and the cost is not guessed. See [Pricing](models-and-providers.md#pricing-unknown-stays-unknown).

## The report card

The report card is the answer. You get it from `GET /api/bench/evaluations/<id>/report`, or for the latest finished results of one agent, version and model from `GET /api/bench/agents/<id>/report-card?version=N&model=M`. In the app it is the evaluation page once runs finish, and the "Report card" tab on the agent page.

It has five parts.

**1. Arms.** Which setups were tested: label, agent, version, model.

**2. By skill (`capabilities`).** Benchmarks are grouped by the skill they test (terminal, coding, memory, support, research, voice, safety, team, gpu, taken from the benchmark's category or track). For each skill and each arm you get one set of numbers over all runs of all benchmarks in that group.

**3. By benchmark (`benchmarks`).** The same numbers per benchmark, with the comparison id (so you can open every run) and, for A/B modes, the verdict.

**4. Reliability.** For each fault, each arm's score under that fault and the `drop`: score under the fault minus score under normal conditions. A drop of -0.21 means the agent solved 21 points fewer tasks when the fault was on.

**5. Safety.** `attack_resisted` is the share of runs under attack where the agent did not do what the attacker wanted. `cheat_rate` is the share of runs where a safety check caught cheating (for example, reading the hidden checks).

Plus **overall**: who won more benchmarks, as counts and one sentence.

### How each number is computed

Every number comes from the measurements Heimdall already records for each run (`metrics/`) and the statistics in `stats/`. Runs that broke for infrastructure reasons (`aborted_infra`, `infra_error`) are not the agent's fault and are not counted.

| Field | How it is computed |
|---|---|
| `score` | Success rate: runs that passed the hidden checks (and every safety check) divided by counted runs. |
| `ci_low`, `ci_high` | 95% interval for the score from a task-clustered bootstrap: tasks are resampled (each with all its trials), because repeated trials of one task are not independent evidence. With one task it falls back to the exact Clopper-Pearson interval over runs. With few tasks the interval is wide; that is the honest answer. |
| `runs` | Counted runs for that arm. |
| `cost_usd` | Sum of each run's cost, priced from the pinned price list using the token counts Bifrost recorded. `null` (shown as "Not known", never $0) when any run's cost is unknown; `cost_runs` counts the runs whose cost is known and `cost_note` says why the others are not. A model the price list does not have (a custom endpoint) is priced from the price set on its key in Models & keys (input, output and cached input per 1M tokens). |
| `tokens` | Sum of input and output tokens Bifrost recorded for the arm's model calls; `null` when any run's count is unknown (`token_runs` counts the known ones). |
| `tasks` | Distinct tasks with a counted run. |
| `steps` | Median agent steps per run. |
| `tool_calls` | Median tool calls per run. |
| `wall_s` | Median wall time per run, in seconds. |
| `drop` (reliability) | Score under the fault minus score under normal conditions, same arm. |
| `attack_resisted` | Share of attacked runs with no unsafe outcome (`safesuccess`). |
| `cheat_rate` | Share of runs where a safety check found cheating (`cheatingrate`). |

### A/B verdict rules

For `versions`, `models` and `opponent` modes, each benchmark gets a verdict from that benchmark's comparison, using the same rules as any Heimdall comparison:

- Both arms run the **same tasks**, so the test is **paired by task**: for each task, the difference between B's and A's success rate over its trials.
- The test is an exact **paired sign-flip test** at alpha 0.05. Tasks with no difference stay in the count and cannot move the result.
- `significant: true` only when the test says the difference is real. Then `winner` is the arm with the higher score and `delta` is B's score minus A's (0.08 = 8 points).
- Otherwise the result is **no clear winner yet** (`winner` is `tie`, `significant: false`). That usually means too few tasks: with fewer than 6 tasks no difference can ever be called real, and 8 tasks can only show a large one. Add tasks or trials.
- An evaluation tests many benchmarks at once, so each benchmark's p-value is corrected for that (Holm's method across the evaluation's benchmarks, shown as `p_value_holm`). A benchmark is `significant` only after the correction; a gap that was just under 0.05 on its own can become "no clear winner yet".
- If something broke the measurement (for example a gate that makes the comparison invalid, or too many infrastructure failures), the benchmark has no winner and its summary says the test was broken. It does not count as a win for either side.
- **Overall** counts benchmarks: `wins` per arm (significant after the correction only) plus ties. The summary reads like "B is better on 3 of 6 benchmarks, worse on 1". Heimdall does not average scores across different benchmarks into one number, because a point on one benchmark is not worth the same as a point on another.

In `score` mode there is one arm, so there are no verdicts, only the numbers: each benchmark's experiment page shows the score with its 95% interval instead of an A against B comparison.

## Example: from the command line, through the API

The API is under `/api/bench`. Reads are open on a local server; writes need an API token (`Authorization: Bearer hm_...`, see [auth.md](auth.md)). The examples use a local server at `http://127.0.0.1:8090` and the token in `$HEIMDALL_TOKEN`.

Check which benchmarks your agent can run:

```bash
curl -sS http://127.0.0.1:8090/api/bench/agents/codex/compatibility
# {"agent":"codex","capabilities":["shell","tools"],
#  "benchmarks":[{"id":"hd-harbor-tb2","ok":true,"requires":["shell"],"missing":[],"reason":""}, ...]}
```

List packs:

```bash
curl -sS http://127.0.0.1:8090/api/bench/evaluation-packs
# [{"id":"coding","name":"Coding agent","benchmarks":["hd-harbor-tb2","hd-swe-verified","hd-swe-ib-lcb"],"capability":"coding"}, ...]
```

Estimate, then launch: is version 2 of my agent better than version 1 on the coding pack, with a model outage?

```bash
cat > ev.json <<'EOF'
{
  "name": "codex v1 vs v2, coding",
  "mode": "versions",
  "arms": [
    {"label": "A", "agent": "codex", "version": 1, "model": "gpt-6.2-sol"},
    {"label": "B", "agent": "codex", "version": 2, "model": "gpt-6.2-sol"}
  ],
  "pack": "coding",
  "tasks_per_benchmark": 8,
  "trials": 2,
  "faults": ["clean", "model-down"],
  "budget_usd": 20
}
EOF

curl -sS -X POST http://127.0.0.1:8090/api/bench/evaluations/estimate \
  -H "Authorization: Bearer $HEIMDALL_TOKEN" -H 'Content-Type: application/json' -d @ev.json
# {"runs":160,"est_cost_usd":{"low":..,"high":..,"known":true},"est_minutes":{"low":..,"high":..},"per_benchmark":[...]}

curl -sS -X POST http://127.0.0.1:8090/api/bench/evaluations \
  -H "Authorization: Bearer $HEIMDALL_TOKEN" -H 'Content-Type: application/json' -d @ev.json
# 202 {"id":"ev_..","spec":{...},"status":"queued","children":[{"benchmark":"hd-harbor-tb2","experiment_id":"exp_..","status":"queued"}, ...]}
```

Follow it, read the report card, or stop it:

```bash
curl -sS http://127.0.0.1:8090/api/bench/evaluations                    # newest first
curl -sS http://127.0.0.1:8090/api/bench/evaluations/ev_...             # live status from its children
curl -sS http://127.0.0.1:8090/api/bench/evaluations/ev_.../report      # the report card
curl -sS -X POST -H "Authorization: Bearer $HEIMDALL_TOKEN" \
  http://127.0.0.1:8090/api/bench/evaluations/ev_.../cancel
```

A report card looks like this (trimmed):

```json
{"evaluation":"ev_..","arms":[{"label":"A","agent":"codex","version":2,"model":"gpt-6.2-sol"}],
 "capabilities":[{"capability":"terminal","benchmarks":["hd-harbor-tb2"],
   "arms":{"A":{"score":0.41,"ci_low":0.30,"ci_high":0.52,"runs":30,"cost_usd":3.2,"tokens":123456,"steps":34,"tool_calls":21,"wall_s":410}}}],
 "benchmarks":[{"benchmark":"hd-harbor-tb2","capability":"terminal","experiment_id":"exp_..","arms":{},
   "verdict":{"winner":"B","delta":0.08,"significant":true,"summary":"B solves 8 points more"}}],
 "reliability":[{"fault":"model-down","arms":{"A":{"score":0.2,"drop":-0.21}}}],
 "safety":{"attack_resisted":{"A":0.88},"cheat_rate":{"A":0.05}},
 "overall":{"winner":"B","wins":{"A":1,"B":3,"tie":2},"summary":"B is better on 3 of 6 benchmarks, worse on 1"}}
```

Each child is an ordinary comparison, so every CLI command that reads comparisons works on it: for example `bifrost-bench leaderboard` and the comparison pages in the app.

## Presets

The wizard offers three sizes of the same setup, each with its runs, time, cost range and the smallest change it can detect (in points, from the power check):

| Preset | Tasks per benchmark | Trials | Faults | Smallest change it can see on one benchmark |
|---|---|---|---|---|
| Quick look (default) | 10 | 1 | clean only | about 30 points |
| Standard | 30 | 1 | clean only | about 16 points |
| Thorough | 50 | 2 | clean, model down, broken tool | about 12 points |

A benchmark with fewer ready tasks runs all of them. `POST /evaluations/estimate` with `"presets": true` returns the three sizes in `presets`.

## Budget

Every benchmark runs under a spend cap, and the cap is always shown. With no `budget_usd` the cap is $5 per benchmark. The wizard does not leave it empty: it fills in the estimate's `budget.suggested_usd`, which covers the high cost estimate with 25% headroom, never less than the $5 per benchmark default and never more than the organization's maximum (`HEIMDALL_EVAL_BUDGET_MAX_USD` on the server, default $100, lowered to the organization's daily spend cap when a server admin set one). You can type any budget up to $1000.

The estimate's `budget` block says which cap applies (`budget_usd`, `per_benchmark_usd`, `default`), whether the cost fits it (`fit`: `fits`, `may_exceed`, `exceeds` or `unknown`) and how many runs the cap would leave unstarted at the low and the high cost estimate (`runs_skipped_low`, `runs_skipped_high`).

When the cap stops a benchmark, its row on the evaluation page says so, with its spend, its cap and the runs it never started (`budget_stopped`, `spent_usd`, `budget_usd`, `not_started` on each child). "Raise budget and continue" (`POST /evaluations/{id}/raise-budget` with `{"budget_usd": <new total>}`) gives every benchmark that is still running or was stopped an equal share of the new budget, raises the live cap of the running ones, and continues the stopped ones from their finished runs; nothing that already ran runs again.

## First estimates

A brand-new agent has no runs, so its estimate rests on the library: sealed runs of other agents on the same benchmark version (your organization's own, plus the platform's reference runs when the server sets `HEIMDALL_LIBRARY_RUNS_ROOT`). Time is their wall time; cost is their recorded cost repriced from the model each run used to your model with the pinned price list. Your agent may differ, so the range is widened by half each way and the estimate says "First run, estimate from library data" (`from_library: true`). After your first evaluation the estimate uses your own runs instead. A run on a model without a price is left out of the cost, never guessed.

## Bare-model baseline

"Compare with the bare model" adds the same model with no agent scaffolding as the opponent: `agents/bare-model.yaml`, one model call with the task text, no tools and no loop, on your agent's model. It answers "does my agent add anything over the model?". It can only answer in text, so it runs on benchmarks graded on an answer (question sets, research answers) and is left out, with the reason, on benchmarks that grade a code change, a tool's state or files.

## Worker slots and images

On the shared worker an organization runs up to 4 runs at once by default (its max concurrent runs, set by a server admin); every estimate shows the slots it assumes (`slots`). Benchmarks of one evaluation run side by side within those slots. On a server that runs evaluations itself, the images a benchmark needs are pulled or built in the background as soon as the evaluation is created, so the first runs do not each wait for them; the shared worker makes every task image of a benchmark present when it first plans it.

## Agent leaderboard

`GET /api/bench/leaderboard/agents?capability=&model=` lists agents (each agent, version and model is one row) with their scores per skill, ranked by the chosen skill (`capability=`); without one they are listed by name, since scores on different skills are never averaged into one number. Each row has cost and the last evaluation. Only your organization's real runs count (practice examples and practice-model runs never appear). A skill score is ranked only once it has at least 10 runs over 5 tasks (`min_runs`, `min_tasks`); smaller samples are shown with "not enough runs" and no rank. Its 95% range is the same task-clustered bootstrap as the report card. A row's cost is "not known" (with the count of unpriced runs) unless every run's cost is known. Built-in agents (codex, claude-code, mini-swe-agent, opencode, gemini-cli) are marked as baselines, so you can see where your agent stands against well-known ones. In the app this is the "Agents" tab of `/leaderboard`, which opens by default.

## Limits

- Ten tasks per benchmark (the default) is enough to spot a big gap, not a small one. Most A/B results on few tasks will be "no clear winner yet". That is correct, not a bug.
- A pack only includes benchmarks that are present and ready in your library or organization.
- `misleading-instruction` only runs where the benchmark has that stress test.
- Cost is only shown when every model's price is known.
