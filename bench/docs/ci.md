# CI gate

Bifrost Bench ships a pull-request gate: every pull request that touches an agent (its config, prompt, notes file, model or tools), an experiment, a release policy, a benchmark or the bench code runs `bifrost-bench check`, and a bad verdict or a policy block fails the build before merge.

| Piece | Path |
|---|---|
| Workflow | `.github/workflows/bench-check.yml` (repo root) |
| Composite action | `bench/ci/action.yml` |
| Gate script | `bench/ci/check.sh` |
| Seal guard | `bench/ci/ci_test.go` |
| Recorded bundle | `bench/testdata/recorded/` |

## What the gate checks

`bifrost-bench check` analyzes an experiment's sealed runs with the experiment's verdict policy (the exact sign-flip test on the primary endpoint, guards and invalid gates) and ends in one verdict:

| Verdict | Action | Default gate |
|---|---|---|
| `improved` | `ship` | pass |
| `inconclusive` | `no_decision` | pass (add `inconclusive` to fail-on to require a decision) |
| `regressed` | `hold` | **fail** |
| `invalid` | `fix_harness` | **fail** |

With a release policy (`-policy`, below), a `block` decision fails the gate whatever the verdict. A candidate that is better on average but breaks a block-severity check does not merge.

The job also verifies the recorded bundle's seals first: `check -exp` reads sealed runs without re-hashing them, so `go test ./ci/` recomputes every file hash and the `seals.log` chain and fails on any changed, added or removed file.

## When it runs

On `pull_request` when any of these change, and on `workflow_dispatch`:

```
bench/agents/**  bench/experiments/**  bench/policies/**  bench/benchmarks/**  bench/**/*.go
bench/ci/**  bench/testdata/recorded/**  .github/workflows/bench-check.yml
```

The job has `permissions: contents: read`, checks out without persisted credentials and uses no secret.

## Recorded and live mode

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

The bundle checked in today is the **synthetic** smoke run (toy agent, scripted model, real Bifrost v2.5.1); its numbers are not findings. A real recorded run will join it. See `testdata/recorded/README.md`.

## Exit codes

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

## Adding the release policy

Policies are YAML files in `bench/policies/` (`default.yaml`, `strict.yaml`), evaluated by `bifrost-bench check -policy <file>`. A policy only adds requirements; the integrity gates always run.

- Leave the action's `policy` input empty and the gate passes `-policy policies/default.yaml` when that file exists and `check -h` lists `-policy`. Name another file to use it, or `none` to turn policy off.
- `check.sh` detects the flag from `bifrost-bench check -h`. A binary without it runs the gate without the policy and says so in the job summary (a warning annotation when the policy was named explicitly), so the workflow works before and after policy-as-code lands.
- An experiment can also reference its policy (`policy: policies/default.yaml`) so the policy hash is frozen in its manifest.
- `policies/**` is a trigger path: a pull request that loosens a policy runs the gate.

**Small samples warn, they do not block.** The default policy leaves `on_unknown` unset, so a check with too few observations is unknown and only warns ("needs review"); a real fail, or an unknown for any other reason, still blocks. On the 8-run smoke bundle, `false-completion-ceiling` sees 4 observed runs against `min_n: 10`, so the decision is `warn` and the recorded job exits 0. `policies/strict.yaml` sets `on_unknown: block` for teams that want small samples to block.

## Wiring provider keys for live runs

The bench is built so the provider key lives in one place: the fault proxy container, which swaps it in for the slot placeholders. Bifrost, the bench process, the agents and the run folders never see it. Keep it that way in CI:

- Store keys as repository or, better, environment secrets (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`). An environment (`bench-live`) can require a reviewer before the job gets them.
- Hand the key only to the step that starts the stack (`make up`). Docker Compose passes it to the fault proxy. Do not write it into `deploy/.env`, the experiment file, the check step's `env`, or a log.
- Let `make env` generate the gateway setup token, the Postgres password and the fault proxy token fresh for each job (`deploy/.env`, mode 600). The check step reads them with `BENCH_CI_ENV_FILE=deploy/.env`.
- Run live jobs only on a self-hosted runner you control, never for pull requests from forks (`github.event.pull_request.head.repo.full_name == github.repository`), and never from `pull_request_target` with the pull request's code checked out: that would run untrusted code next to the key.
- Check out with `persist-credentials: false`, keep `permissions: contents: read`, and stop the stack in an `if: always()` step (`make down`).

The workflow carries a commented-out `live` job that follows these rules: start the stack with the key, `make doctor`, run `experiments/codewins.reduced.yaml` with `policies/default.yaml`, stop the stack.

## Using the action

```yaml
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
  with:
    persist-credentials: false
- id: gate
  uses: ./bench/ci
  with:
    mode: recorded            # or live
    exp-id: e_smoke_sdoj      # recorded: which bundled experiment
    data-mode: synthetic      # recorded: label for a scripted-model bundle
    experiment: ""            # live: experiments/<file>.yaml
    policy: ""                # empty: policies/default.yaml when supported; none: off
    fail-on: regressed,invalid
- run: echo "verdict ${{ steps.gate.outputs.verdict }}, exit ${{ steps.gate.outputs.exit-code }}"
  if: always()
```

Outputs: `verdict`, `action`, `policy-decision`, `exit-code`, `report` (path on the runner). The report and `report.json` are uploaded as the `bench-check-report` artifact, and the report is appended to the job summary.

Locally, from `bench/`: `BENCH_CI_DATA_MODE=synthetic ci/check.sh recorded`. Every input is a `BENCH_CI_*` variable listed at the top of `check.sh`.

## Sample job summary

From the recorded job on the synthetic bundle with the default policy, abridged:

```markdown
## Bifrost Bench gate: fail: policy decision block

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

## Primary endpoint

| Field | Value |
|---|---|
| Endpoint | clean / verified_success |
| Decision | INCONCLUSIVE |
| d_bar | +0.0 pp |
| p-value | 1.0000 |
| Discordant tasks | 0 of 2 |
...

## Release policy

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

## In your own repository: `heimdall ci install github`

`heimdall ci install github` (step 4 of `docs/quickstart.md`) writes
`.github/workflows/heimdall.yml` into the repository holding your
`.heimdall` project. `-dry-run` prints it and writes nothing.

| Mode | Runner | What the job does |
|---|---|---|
| `-mode recorded` (default) | GitHub-hosted | re-checks the sealed experiment you commit under `.heimdall/recorded/` (copy one from `.heimdall/runs/<exp_id>`): no Docker, no gateway, no key, no secret |
| `-mode live` | self-hosted, label `heimdall` | writes a baseline-vs-candidate experiment (`heimdall compare --baseline origin/<base branch> --candidate <PR head> --write-only`) and runs it with `check` against the stack on that runner. When both refs have the same agent files (a pull request that changes only suite data or the workflow), compare exits 6 and the job skips the gate and passes |

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
