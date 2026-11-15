# Agent diff

The agent diff answers the daily question "what changed between version 1 and version 2 of my agent, and which tasks flipped?". It puts two resolved agent configs side by side, field by field, and compares how they did on the tasks both of them ran. It is a reading of evidence already sealed: nothing runs and nothing is written.

## Where it lives

| Part | Path |
|---|---|
| Compute | `agentdiff/` (pure over `schemas` types: reads manifests, runs, `metrics.json`, `gates.json` and `proxy.ledger.jsonl` through `agentdiff.Source`, and notes files through an optional `agentdiff.ContentSource`) |
| API | `GET /api/bench/agents/diff?a=&b=&suite=&condition=&agent=&synthetic=` returns `agentdiff.Report` plus `mode`; `GET /api/bench/agents/diff/versions` returns `agentdiff.VersionList` plus `mode` for the picker; `handlers/agentdiff.go` |
| CLI | `bifrost-bench agent diff -a <hash\|exp_id:arm> -b <hash\|exp_id:arm> [-suite id@version] [-condition clean] [-synthetic] [-json] [-config deploy/bench.yaml] [-runs <dir>]`, `cmd/bifrost-bench/agentdiff.go` |
| UI | `/agents/diff` (`ui/app/agents_/diff/`, `ui/components/agentdiff/`, endpoints in `ui/lib/store/apis/agentDiffApi.ts`): a picker of two versions (a leaderboard link sets B and A defaults to the same agent's next older version), the summary, the config diff by group, the flipped tasks with links to the run comparison view, metric deltas and gates. Linked from the Agents page ("Compare versions") and from each leaderboard entry ("Diff") |
| Contract | `agentdiff/agentdiff.go`, `handlers/testdata/contract/AgentDiff*.keys`, `ui/lib/types/bench.ts` |

## Picking the two versions

A version is one resolved agent config hash (the manifest's `arms[x].config_hash`). Three ways to name one:

| Form | Example | Runs that count |
|---|---|---|
| Config hash, or a unique prefix of 8 or more hex characters | `-a dfd6f713e64a` | every verified run of that hash on the suite and condition, pooled across experiments, as on the leaderboard |
| Experiment arm `<exp_id>:<arm>` | `-a e_pilot_mixed_nc93:A` | only that arm's runs in that experiment |
| Agent id, with no `-a` and `-b` | `-agent codex -suite coding-core@1.0.0` | B is the agent's newest config hash on the suite, A the one before it (by manifest creation time; arms of one manifest tie and the later arm id counts as newer) |

The suite defaults to the newest suite version both versions ran (an arm pins its own experiment's suite); the condition defaults to `clean`. An unknown hash or arm, an ambiguous prefix, two versions that never ran the same suite version, or two versions with **no common task** are refused with a message that says what each version ran and what was left out (API 400, CLI exit 64). A config diff alone cannot say which tasks flipped, so it is not returned on its own.

## Config diff

The resolved configs come from the manifests, so the diff shows what actually ran, after every `set:` override. Each field is a dotted path of the config's JSON form (`model`, `env.BENCH_MODEL`, `budgets.cpus`, `skills.extra`), plus the manifest's `image_id`, `task_images.*`, `budget_snapshot.*` and the canonical arm identity fields the config does not already carry (`identity.adapter_version`, `identity.agent_version`, `identity.image_digest`, `identity.parser_version`). Each change is `changed`, `added` or `removed`, with the old and new value. Unset and empty values compare equal.

Changes are grouped: model, prompt and notes, tools/MCP/skills, budgets, gateway policy, image, other. Environment variables are grouped by name (a name with `MODEL` goes to model, `PROMPT`, `INSTRUCTION` or `NOTES` to prompt and notes, `MCP`, `TOOL` or `SKILL` to tools).

Secrets: an environment value whose name contains `KEY`, `TOKEN`, `SECRET`, `PASSWORD` or `AUTH` is shown as `[redacted]` on both sides (the planner's rule for the environment policy hash). A changed secret still shows as a change, never its value. Every other string, and every line of a text diff, passes through `utils.RedactSecrets`.

Files are compared by the hashes the planner laid down for the condition (`arms[x].files`; clean, or the first condition, when the arm has none for it). Task-keyed files (`<task>:<path>`) are compared only for tasks both arms list, so a different task selection is not reported as a config change. A changed file gets a unified text diff when both contents are at hand: the config's own file templates (`files:` in the agent YAML, such as a prompt or a CLI config), or the notes user file read from the bench root, and only when it still hashes as planned. A file over 64 KiB, a binary file, or one whose content is not on disk is compared by hash only, and the change says why. A diff is cut after 400 lines and says so.

## Outcome diff

Only **verified sealed runs** count. For every slot of each version on the suite and condition, the counted run is picked as the store picks it, then left out and counted by reason when its experiment's newest verdict is invalid, its experiment called external APIs live, it is synthetic, it is not sealed, its slot has more than one counted run, its outcome does not score, or its files no longer match its seal (`VerifySeal` through the store's verified index). Synthetic runs (a scripted agent, the scripted model, or a smoke experiment, decided as the leaderboard decides it) are left out unless `-synthetic` (API `synthetic=1`) is given. Grades are the runs' original grades.

A run passes when its `verified_success` metric is at least 0.5, or, without the metric, when its outcome is `pass` (a timeout or a crash is a failure). Per common task each version has a pass rate. A version passes a task **stably** when every counted trial passed and fails it stably when none did. Classes:

| Class | Meaning |
|---|---|
| `flipped_to_pass` | A failed stably, B passes stably |
| `flipped_to_fail` | A passed stably, B fails stably |
| `unchanged_pass`, `unchanged_fail` | both stable, same result |
| `unstable_in_both` | neither is stable |
| `moved_to_pass`, `moved_to_fail` | one side is unstable and the rate moved: not a clean flip |

`net` is flips to pass minus flips to fail. The exact paired sign-flip test (`stats.SignFlip`) runs on the per-task pass rate differences, B minus A. When the changed tasks are too few for any result to reach p below alpha (`stats.MinPossibleP`: 5 or fewer changed tasks at 0.05), the conclusion is `cannot_tell`, never "no difference". Otherwise it is `b_better`, `b_worse` or `no_clear_difference`.

Metric deltas: `cost_usd`, `tokens`, `steps`, `wall_time_s` and `false_completion`, paired by task (the mean of a task's observed values per version, then the mean over the tasks observed on both sides). Unknown and not-applicable values are counted, never read as zero. Direction reads B minus A through `verdict.HigherIsBetter`: lower cost, tokens, steps, time and false completion are better.

Gates: every gate that fired in either version on the common tasks, with its firing counts. A gate is **new** when it fired in B and never in A; the tasks where it fired in B and not in A are listed.

Each flipped or moved task carries a representative run pair: a run of A and a run of B with the outcomes that show the change, from the same experiment pair when there is one, so the run comparison view (`/runs?pair=<pair_id>`) shows them side by side. When the two runs come from different experiments, each run's own pair is linked.

The summary is a few plain sentences: pass counts, flips and net change, what the sign-flip test can and cannot say, which config groups changed, and how mean cost moved.

## Example (the recorded real experiment)

```
$ bifrost-bench agent diff -runs testdata/recorded -a e_pilot_mixed_nc93:A -b e_pilot_mixed_nc93:B -condition empty-test-run
agent diff  coding-core@1.0.0  condition empty-test-run
A  codex  codex gpt-5.4-mini  dfd6f713e64a  (e_pilot_mixed_nc93:A)  1 verified run(s)
B  custom-python  custom-python gpt-5.4-mini  8bbc91c3b83d  (e_pilot_mixed_nc93:B)  1 verified run(s)

On 1 common task of coding-core@1.0.0 under empty-test-run, B passes 1 and A passes 0. 1 task flipped to pass and 0 flipped to fail (net +1). Cannot tell whether B is better or worse: 1 task changed, and at least 6 must change before any result can reach p < 0.05. The config changed in 20 places (model, prompt and notes, budgets, image, other). B costs $0.0005 more per run on average ($0.0217 against $0.0213).
...
tasks: 1 common
  flipped_to_pass   t05                      A 0/1  B 1/1  pair p_uwpe1pdp9o
```

`-runs <dir>` reads another runs root (here the read-only recorded bundle); without it the config's `runs_root` is read and notes files are read from the bench root.

## Limits

- Original grades only; a regrade is not read.
- Text diffs cover the config's own file templates and the notes user file. Task workspace files and skills are compared by hash only.
- A version picked by hash pools every experiment of that hash, so runs planned under different experiment settings (trials, seeds) pool too, as on the leaderboard. Pick by `<exp_id>:<arm>` for one experiment only.
- The text diff is served through the API after secret redaction. Redaction catches the usual key forms (`api_key=...`, `Authorization: ...`, JSON secret fields); a secret in another shape inside a prompt template would show.

## Removing it

Delete `agentdiff/`, `cmd/bifrost-bench/agentdiff.go` and `agentdiff_test.go`, `handlers/agentdiff.go` and `agentdiff_test.go`, `handlers/testdata/contract/AgentDiff*.keys`, `ui/app/agents_/`, `ui/components/agentdiff/`, `ui/lib/store/apis/agentDiffApi.ts`, `ui/public/fixtures/agents.diff.json`, `ui/public/fixtures/agents.diff.versions.json` and this file. Then remove the lines elsewhere that register it:

- `cmd/bifrost-bench/agent.go`: the `case "diff":` clause.
- `server/server.go`: the `NewAgentDiffHandler` call and the comment line above it.
- `handlers/openapi.go`: the `AgentDiffRouteDoc, AgentDiffVersionsRouteDoc` line.
- `ui/lib/types/bench.ts`: the agent diff block at the end; `ui/locales/en/bench.json`: the `agentDiff` key.
- `ui/app/agents/views/agentsView.tsx`: the `CompareVersionsLink` import and its one use; `ui/components/leaderboard/leaderboardCells.tsx`: the "Diff" link in `AgentCell`.
- `ui/lib/store/apis/fixtures.ts`: the two agent diff fixture routes and their comment; `ui/lib/store/apis/fixtures.test.ts`: the two resolve cases and the two fixture key checks.
- `README.md`: the Agent diff row.
