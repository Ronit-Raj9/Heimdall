# Test your agent on every pull request

Heimdall gates agent changes in CI. On every pull request (GitHub) or merge request (GitLab) it:

1. registers the pull request's agent config as a new **version** of your agent on your hosted Heimdall (or takes the live config with a new image digest or preview endpoint);
2. launches an **evaluation** in versions mode: the pull request's version against the **live version** (the last released version; before any release, the version of the agent's saved config), on the benchmark pack or benchmarks you pick, clean and under the faults you pick;
3. waits for it, fetches the **report card** and applies the **evaluation gate**;
4. posts the report card summary as one pull-request comment (edited in place on later pushes) with a link to the full report card;
5. sets a commit status ("Heimdall evaluation (agent)") and fails the job when the gate fails.

The fastest way to set it up is the agent page: open your agent, tab **Pull requests**. It generates the workflow file for GitHub Actions or GitLab CI, creates the CI token and lists the variables to add.

| Piece | Path |
|---|---|
| CLI command (used by both) | `bifrost-bench ci evaluate` (`cmd/bifrost-bench/cievaluate.go`) |
| GitHub Action | `bench/ci/action.yml`, mode `evaluate` (the default) |
| GitLab CI template | `bench/ci/heimdall.gitlab-ci.yml` |
| Workflow generator | `heimdall ci install github\|gitlab -agent <id>` and `GET /api/bench/integrations/ci/workflow?mode=hosted` |
| Evaluation gate | `bench/ci/evalgate.go` |

## Setup

1. **An org API token.** In Heimdall, Settings > API tokens, create a token with the member role (or use "Create a CI token" on the agent page). It is shown once.
2. **Secrets and variables.**
   - GitHub: repository Settings > Secrets and variables > Actions. Secret `HEIMDALL_TOKEN`; variable `HEIMDALL_URL` (your Heimdall's address) unless the workflow names it.
   - GitLab: project Settings > CI/CD > Variables. `HEIMDALL_TOKEN` (masked), `HEIMDALL_URL`, and `GITLAB_TOKEN`: a project access token with the `api` scope. GitLab's CI job token cannot write merge request notes or commit statuses.
3. **The workflow file.** Commit what the agent page generates, or write it with the CLI:

```bash
heimdall ci install github -agent my-agent -pack coding -fault model-down   # .github/workflows/heimdall-evaluate.yml
heimdall ci install gitlab -agent my-agent -benchmark hd-harbor-tb2        # .gitlab/heimdall-evaluate.gitlab-ci.yml
```

`-config` names the agent config file in your repository (default `agents/<agent>.yaml`); `-image` or `-endpoint` makes a workflow that passes an image or a preview URL instead; `-url` writes your Heimdall's address into the file; `-dry-run` prints it.

Neither file holds a secret. GitHub gives pull requests from forks no secrets, so the job skips them.

## GitHub Action

```yaml
permissions:
  contents: read
  pull-requests: write # the report card comment
  statuses: write # the commit status

jobs:
  evaluate:
    if: github.event_name == 'workflow_dispatch' || github.event.pull_request.head.repo.full_name == github.repository
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
        with:
          persist-credentials: false
      - uses: Ronit-Raj9/Heimdall/bench/ci@main # pin a commit
        with:
          mode: evaluate
          url: ${{ vars.HEIMDALL_URL }}
          token: ${{ secrets.HEIMDALL_TOKEN }}
          agent: my-agent
          config: agents/my-agent.yaml
          pack: coding
          faults: model-down,broken-tool
```

The action builds `bifrost-bench` from its own checkout and runs `ci evaluate`. Every input reaches the command as a `HEIMDALL_*` environment variable, never spliced into the shell line.

| Input | Meaning |
|---|---|
| `url`, `token` | your Heimdall and the org API token (required) |
| `agent` | the agent id on Heimdall (required) |
| `config` / `image` / `endpoint` | what the pull request changes: a config file in the repository, an image it built (`ref@sha256:...`), or a preview deployment URL (remote agents). Image and endpoint patch the live config |
| `baseline` | `live` (default) or a version number |
| `model` | model for both versions; empty uses each version's own model |
| `pack`, `benchmarks` | benchmark pack and/or comma-separated benchmark ids |
| `faults` | comma-separated: `model-down`, `broken-tool`, `misleading-instruction` (clean always runs) |
| `trials`, `tasks`, `budget-usd` | repeats per task, tasks per benchmark, spend cap; empty uses the server defaults |
| `fail-on` | outcomes that fail: `regressed`, `inconclusive`, `failed` (default `regressed,failed`) |
| `max-score-drop`, `max-cost-increase` | extra limits: fail when a benchmark's score falls by more than this (0.05 = 5 points) or total cost grows by more than this fraction |
| `timeout` | how long to wait (default `3h`) |
| `github-token` | token for the comment and status (default the job's token) |

Outputs: `outcome`, `evaluation`, `report-url`, `version`, `exit-code`. `report.json`, `gate.json` and `comment.md` are uploaded as the `heimdall-report` artifact, and the comment is added to the job summary.

## GitLab CI

Include the template and extend its hidden job:

```yaml
include:
  - remote: https://raw.githubusercontent.com/Ronit-Raj9/Heimdall/main/bench/ci/heimdall.gitlab-ci.yml

heimdall-evaluate:
  extends: .heimdall-evaluate
  variables:
    HEIMDALL_AGENT: my-agent
    HEIMDALL_CONFIG: agents/my-agent.yaml
    HEIMDALL_PACK: coding
    HEIMDALL_FAULTS: model-down
```

Or commit the standalone file `heimdall ci install gitlab` (or the agent page) writes and include it with `include: - local: .gitlab/heimdall-evaluate.gitlab-ci.yml`. The job runs on merge request pipelines, is `interruptible` (a newer push cancels it, and `ci evaluate` then cancels the evaluation), and keeps `heimdall-report/` as an artifact.

## `bifrost-bench ci evaluate`

```
bifrost-bench ci evaluate -agent <id> [-config <file> | -image <ref@sha256:...> | -endpoint <url>]
    [-url https://heimdall.example.com] [-pack coding] [-benchmark <id>]... [-fault model-down]...
    [-baseline live|<version>] [-model <model>] [-trials 2] [-tasks 8] [-budget-usd 20]
    [-fail-on regressed,failed] [-max-score-drop 0.05] [-max-cost-increase 0.25]
    [-timeout 3h] [-poll 20s] [-out <dir>] [-provider auto|github|gitlab|none] [-no-comment] [-no-status]
```

- The token is read from `HEIMDALL_TOKEN` only, never a flag, and is refused over plain `http` to anything but loopback.
- Every flag has a `HEIMDALL_<NAME>` fallback (`HEIMDALL_URL`, `HEIMDALL_AGENT`, `HEIMDALL_BENCHMARKS=a,b`, `HEIMDALL_FAULTS`, `HEIMDALL_FAIL_ON`, ...).
- `-provider auto` reads the CI environment: on GitHub the pull request number and head commit come from the event payload and the comment and status use `GITHUB_TOKEN`; on GitLab from `CI_MERGE_REQUEST_IID`, `CI_PROJECT_ID` and `GITLAB_TOKEN`. Without a token or a pull request it still runs and gates; it only skips the comment and status. A failed comment or status is a warning, never a gate failure.
- Run it from your laptop to try a config before opening a pull request: `HEIMDALL_TOKEN=hm_... heimdall ci evaluate -url https://... -agent my-agent -config agents/my-agent.yaml -pack coding -provider none`.

What it calls on your Heimdall (all with `Authorization: Bearer <org API token>`):

| Step | API |
|---|---|
| live version | `GET /api/bench/agents/{id}/versions` (`live`: the last released version, else the version holding the saved config; `live_source` says which) and `GET .../versions/{n}` |
| register the pull request's version | `POST /api/bench/agents/{id}/versions` with `{"yaml": "...", "note": "pull request #12 at abc123"}`. The agent's saved config does not change; a config equal to any existing version reuses it |
| launch | `POST /api/bench/evaluations` (mode `versions`, arm A the live version, arm B the pull request) |
| wait | `GET /api/bench/evaluations/{id}` every `-poll` |
| report | `GET /api/bench/evaluations/{id}/report` |
| cancel | `POST /api/bench/evaluations/{id}/cancel` on timeout or when the CI job is cancelled |

When the pull request's config equals the live version there is nothing to evaluate: the comment says "no agent change", the status is success and the job passes without launching anything.

## The evaluation gate

| Outcome | When | Fails by default |
|---|---|---|
| `improved` | better overall, and the gap is real (significant after Holm across benchmarks) | no |
| `same` | about the same on every benchmark (the tie interval is inside the margin) | no |
| `inconclusive` | no real gap either way yet | no (add `inconclusive` to fail-on to require a decision) |
| `regressed` | worse overall, or worse on any benchmark with a real gap, or over `max-score-drop` / `max-cost-increase` | **yes** |
| `failed` | the evaluation failed, was cancelled, or no benchmark produced runs | **yes** |
| `no-change` | the pull request's config is the live version | never |

| Exit code | Meaning |
|---|---|
| 0 | gate passed |
| 1 | regressed (listed in fail-on) |
| 2 | inconclusive (listed in fail-on) |
| 3 | evaluation failed (listed in fail-on) |
| 4 | error: API unreachable, bad token, timeout |
| 64 | usage error |

## The comment

```markdown
### Heimdall: my-agent v7 (this pull request) vs v5 (live)

**Gate failed: worse than the live version.** Why: hd-harbor-tb2: worse by 30 points, and the gap is real.

| Benchmark | Live v5 | This PR v7 | Change | Verdict |
|---|---|---|---|---|
| hd-harbor-tb2 | 60% (50% to 70%, 20 runs) | 30% (20% to 40%, 20 runs) | -30 pts | **worse** |
| Cost | $2.00 | $2.50 | +25% | |

Under faults (score, change from clean):

- model-down: live 50% (-10 pts), this PR 20% (-20 pts)

[Open the full report card](https://heimdall.example.com/evaluations/ev_...) · evaluation `ev_...` · commit `0123456789ab`
```

## Self-hosted gate (recorded and live modes)

The same action also runs `bifrost-bench check` without a hosted Heimdall: `mode: recorded` re-analyzes a sealed experiment bundle you name (the `bundle` input is required; there is no fixture default) and `mode: live` runs an experiment file against a bench stack on a self-hosted runner. This repository's own `.github/workflows/bench-check.yml` uses recorded mode on `bench/testdata/recorded/` to guard the statistics, verdicts and evidence integrity.

### What the gate checks

`bifrost-bench check` analyzes an experiment's sealed runs with the experiment's verdict policy (the exact sign-flip test on the primary endpoint, guards and invalid gates) and ends in one verdict:

| Verdict | Action | Default gate |
|---|---|---|
| `improved` | `ship` | pass |
| `inconclusive` | `no_decision` | pass (add `inconclusive` to fail-on to require a decision) |
| `regressed` | `hold` | **fail** |
| `invalid` | `fix_harness` | **fail** |

With a release policy (`-policy`, below), a `block` decision fails the gate whatever the verdict. A candidate that is better on average but breaks a block-severity check does not merge.

The job also verifies the whole recorded bundle's seals first with `go test ./ci/`: it recomputes every file hash of every sealed run of every experiment in the bundle and checks each `seals.log` chain, failing on any changed, added or removed file. `check -exp` itself verifies the counted runs and the chain of the experiment it analyses (`docs/evidence-integrity.md`); the bundle-wide test also covers the other experiments and any run the selected analysis does not count.

### When it runs

On `pull_request` when any of these change, and on `workflow_dispatch`:

```
bench/agents/**  bench/experiments/**  bench/policies/**  bench/benchmarks/**  bench/**/*.go
bench/ci/**  bench/testdata/recorded/**  .github/workflows/bench-check.yml
```

The job has `permissions: contents: read`, checks out without persisted credentials and uses no secret.

### Recorded and live mode

| | Recorded (the default job) | Live |
|---|---|---|
| What runs | `check -exp <id>`: re-analysis of a sealed experiment in `testdata/recorded/` | `check experiments/<file>.yaml`: plan, freeze, run every slot, analyze |
| Needs | Go only | Docker, the stack from `make up`, its tokens, a provider key in the fault proxy |
| Runner | any GitHub-hosted runner | a self-hosted runner |
| Secrets | none | provider key, from repository or environment secrets |
| Time | about a minute | as long as the experiment |
| `BENCH_SERVER_MODE` | `recorded` (or `synthetic` for a scripted-model bundle) | `live` |

Recorded mode copies the bundle to a temp runs root (`BENCH_RUNS_ROOT`) before `check` writes its analysis folder, so the checked-in bundle never changes.

**What recorded mode does not do:** it runs no agent. A pull request that only edits `agents/*.yaml` gets the same verdict as before, because the evidence is the bundle, not the new agent. Recorded mode guards the bench itself (statistics, verdict policy, reports, release policies) and the integrity of the evidence. To gate an agent change for real, either:

1. run the live job on a self-hosted runner (below), or
2. bring the evidence: run the experiment with the changed agent locally (`bifrost-bench run experiments/<file>.yaml`), copy the sealed `runs/<exp_id>` folder into `testdata/recorded/`, and set `exp-id` in the workflow. CI verifies the seals and re-derives the verdict from the sealed runs; nobody's local report is trusted. Seals are local hashes without an external anchor, so this catches accidents and careless edits, not a determined forger. Live mode is the trustworthy path.

The bundle checked in today holds two experiments: `e_pilot_mixed_nc93`, a **real** 10-run pilot (Codex vs custom-python on `gpt-5.4-mini`), and `e_smoke_sdoj`, a **synthetic** smoke run (toy agent, scripted model, real Bifrost v2.5.1) whose numbers are not findings. With two experiments in the bundle, `exp-id` (`BENCH_CI_EXP_ID`) is required. See `testdata/recorded/README.md`.

### Exit codes

| Code | Meaning | Fails the job |
|---|---|---|
| 0 | Pass | no |
| 1 | Verdict `regressed`, listed in fail-on | yes |
| 2 | Verdict `inconclusive`, listed in fail-on | yes |
| 3 | Verdict `invalid`, listed in fail-on | yes |
| 4 | Infrastructure error: build, gateway, fault proxy, store | yes |
| 5 | Release policy decision `block` | yes |
| 64 | Usage error: bad mode, missing experiment, bundle or policy file | yes |

A verdict not listed in fail-on exits 0. The policy code is checked before the verdict codes.

### Adding the release policy

Policies are YAML files in `bench/policies/` (`default.yaml`, `strict.yaml`), evaluated by `bifrost-bench check -policy <file>`. A policy only adds requirements; the integrity gates always run.

- Leave the action's `policy` input empty and the gate passes `-default-policy policies/default.yaml` when that file exists: it is evaluated only for an experiment that froze no policy, and a frozen policy keeps its hash check. (A binary without `-default-policy` gets `-policy` as before.) Name another file to use it instead of the experiment's frozen policy (an explicit override is not failed by the frozen policy's hash check; drift of the frozen file still is), or `none` to turn policy off, including a policy the experiment froze (`check -policy none`). A binary whose `check -h` does not document `-policy none` cannot turn the frozen policy off; the wrapper then says so in the summary.
- `check.sh` detects the flag from `bifrost-bench check -h`. A binary without it runs the gate without the policy and says so in the job summary (a warning annotation when the policy was named explicitly), so the workflow works before and after policy-as-code lands.
- An experiment can also reference its policy (`policy: policies/default.yaml`) so the policy hash is frozen in its manifest.
- `policies/**` is a trigger path: a pull request that loosens a policy runs the gate.

**Small samples warn, they do not block.** The default policy leaves `on_unknown` unset, so a check with too few observations is unknown and only warns ("needs review"); a real fail, or an unknown for any other reason, still blocks. On the 8-run smoke bundle, `false-completion-ceiling` sees 4 observed runs against `min_n: 10`, so the decision is `warn` and the recorded job exits 0. `policies/strict.yaml` sets `on_unknown: block` for teams that want small samples to block.

### Wiring provider keys for live runs

The bench is built so the provider key lives in one place: the fault proxy container, which swaps it in for the slot placeholders. Bifrost, the bench process, the agents and the run folders never see it. Keep it that way in CI:

- Store keys as repository or, better, environment secrets (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`). An environment (`bench-live`) can require a reviewer before the job gets them.
- Hand the key only to the step that starts the stack (`make up`). Docker Compose passes it to the fault proxy. Do not write it into `deploy/.env`, the experiment file, the check step's `env`, or a log.
- Let `make env` generate the gateway setup token, the Postgres password and the fault proxy token fresh for each job (`deploy/.env`, mode 600). The check step reads them with `BENCH_CI_ENV_FILE=deploy/.env`.
- Run live jobs only on a self-hosted runner you control, never for pull requests from forks (`github.event.pull_request.head.repo.full_name == github.repository`), and never from `pull_request_target` with the pull request's code checked out: that would run untrusted code next to the key.
- Check out with `persist-credentials: false`, keep `permissions: contents: read`, and stop the stack in an `if: always()` step (`make down`).

The workflow carries a commented-out `live` job that follows these rules: start the stack with the key, `make doctor`, run `experiments/codewins.reduced.yaml` with `policies/default.yaml`, stop the stack.

### Using the action

```yaml
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
  with:
    persist-credentials: false
- id: gate
  uses: ./bench/ci
  with:
    mode: recorded            # required: evaluate, recorded or live (no default)
    bundle: testdata/recorded # recorded: required, your sealed bundle folder (this one is Heimdall's own test bundle)
    exp-id: e_smoke_sdoj      # recorded: which bundled experiment
    data-mode: synthetic      # recorded: label for a scripted-model bundle
    experiment: ""            # live: experiments/<file>.yaml
    policy: ""                # empty: policies/default.yaml when supported; none: off
    fail-on: regressed,invalid
- run: echo "verdict ${{ steps.gate.outputs.verdict }}, exit ${{ steps.gate.outputs.exit-code }}"
  if: always()
```

Outputs: `verdict`, `action`, `policy-decision`, `exit-code`, `report` (path on the runner). The report and `report.json` are uploaded as the `bench-check-report` artifact, and the report is appended to the job summary.

The action has no default mode and no default bundle: without `mode`, or in recorded mode without `bundle`, it fails at once with exit 64 and says which input to set, so a gate can never pass by re-checking Heimdall's own test bundle.

Locally, from `bench/`: `BENCH_CI_BUNDLE=testdata/recorded BENCH_CI_EXP_ID=e_smoke_sdoj BENCH_CI_DATA_MODE=synthetic ci/check.sh recorded` for the synthetic smoke, or `BENCH_CI_BUNDLE=testdata/recorded BENCH_CI_EXP_ID=e_pilot_mixed_nc93 ci/check.sh recorded` for the real pilot. Without `BENCH_CI_EXP_ID` the wrapper exits 64, because the bundle holds two experiments. Every input is a `BENCH_CI_*` variable listed at the top of `check.sh`.

### Sample job summary

From the recorded job on the synthetic bundle with the default policy, abridged:

```markdown
### Bifrost Bench gate: fail: policy decision block

| Field | Value |
|---|---|
| Subject | recorded replay of `e_smoke_sdoj` (data: synthetic), no agent ran in this job |
| Verdict | `inconclusive` |
| Action | `no_decision` |
| Policy | `policies/default.yaml`, decision `block` |
| Fails on | `regressed,invalid`, policy block |
| Exit code | 5 |

# Bifrost Bench: INCONCLUSIVE, action NO_DECISION, policy BLOCK [SYNTHETIC]

> SYNTHETIC smoke test: scripted agent and scripted model. These numbers are not findings.

Experiment `e_smoke_sdoj`, analysis `a_ho7u88ns`, grade source `original`.

### Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | +0.0 pp |
| p-value | 1.0000 |
| Discordant tasks | 0 of 2 |
...

### Release policy

Policy `enterprise-default`, hash `277b2be0eaa7`: **BLOCK**. A policy only adds requirements; the baseline gates always run.

| Check | Metric | Condition | Arm | Status | Observed | Threshold | n | Reason |
|---|---|---|---|---|---|---|---|---|
| clean-no-regression | verified_success | clean | B | PASS | 0 | no regression | 2 | not regressed at either bound |
| false-completion-ceiling | false_completion | all | B | UNKNOWN (blocks) | unknown | <= 0.05 | 2 | 2 observed runs of 4, fewer than min_n 10 |
| no-integrity-violations | integrity_violations | all | B | PASS | 0 | = 0 | 4 | 0 of 4 runs had a fired veto gate |
...
```

With `policy: none` the same job passes: `bench gate: verdict=inconclusive action=no_decision policy=n/a exit=0 (pass)`.

The job log ends with one line, e.g. `bench gate: verdict=inconclusive action=no_decision policy=block exit=5 (fail: policy decision block)`, and a failing gate adds an error annotation such as `fail: regressed (verdict regressed, action hold, exit 1)`.

### Recorded and live workflows: `heimdall ci install github -mode recorded|live`

`heimdall ci install github` (step 4 of `docs/quickstart.md`) writes
`.github/workflows/heimdall.yml` into the repository holding your
`.heimdall` project. `-dry-run` prints it and writes nothing.

| Mode | Runner | What the job does |
|---|---|---|
| `-mode recorded` (default) | GitHub-hosted | re-checks the sealed experiment you commit under `.heimdall/recorded/` (copy one from `.heimdall/runs/<exp_id>`): no Docker, no gateway, no key, no secret |
| `-mode live` | self-hosted, label `heimdall` | writes a baseline-vs-candidate experiment (`heimdall compare --baseline origin/<base branch> --candidate <PR head> --budget-usd <cap> --write-only`; the budget must be positive for real agents, because `-write-only` still validates the budget it embeds in the new experiment) and runs it with `check` against the stack on that runner. When both refs have the same agent files (a pull request that changes only suite data or the workflow), compare exits 6 and the job skips the gate and passes |

Both modes:

1. check out the repository (full history) and Heimdall (`-source`,
   `-source-ref`; pin a commit), set up Go and build the binary once;
2. run the gate through this folder's composite action (`uses:
   ./.heimdall-src/bench/ci`) with the prebuilt binary (`bin` input) and the
   project folder (`BENCH_CI_BENCH_DIR=.heimdall`), so `check.sh`, the seal
   guard, the exit codes and the job summary are the ones above;
3. render the verdict card (`heimdall ci card -report <report.json>`):
   decision and action, a data-strength banner, "N of M tasks have
   unreviewed instructions", verified success and cost per verified success
   for both arms, the effect with its interval, the policy, integrity, and the
   three tasks that differ most;
4. post it as one pull-request comment and update that comment on every
   later run (it starts with `<!-- heimdall-verdict -->`; `gh api` with the
   job's `GITHUB_TOKEN`, `pull-requests: write`);
5. upload the evidence (`.heimdall/runs/` in live mode, `.heimdall/recorded/`
   in recorded mode, plus the card) as the `heimdall-evidence` artifact;
6. fail the job on `regressed`, `invalid` or a release-policy block (the
   action's last step).

The workflow names no secret. In live mode the stack's tokens stay on the
self-hosted runner in an env file whose path is the repository variable
`HEIMDALL_ENV_FILE`; `check.sh` sources it without printing it, and provider
keys live only in the stack's fault proxy. Pull requests from forks never
run the live job. The workflow triggers on the files that change a coding
agent: `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.claude/**`, `skills/**`,
`prompts/**`, `.heimdall/**` and the workflow itself.

Not verified here: the workflow has not run on GitHub. The card renderer and
the workflow text are golden-tested (`go test ./ci/`, which also parses the
YAML), the action's new `bin` input is a shell `if`, and the PR comment step
uses documented `gh api` endpoints (`repos/{repo}/issues/{pr}/comments`,
`issues/comments/{id}`). What the job runs was checked locally on the
walk-through's sealed run (docs/quickstart.md, step 4b): the seal guard with
`BENCH_CI_BUNDLE` pointing into `.heimdall/recorded`, `check.sh recorded`
with `BENCH_CI_BENCH_DIR=.heimdall` and a prebuilt binary (exit 5, policy
block), and `heimdall ci card` on its `report.json`.
