# Failure clusters

Failure clusters answer "what keeps going wrong?" across every experiment at once. Each non-passing run gets a **signature**: a stable hash of deterministic signals read from its sealed record. Runs that share a signature form a cluster, so a team reads ten clusters instead of three hundred runs. No model and no embedding is involved: the same runs always give the same clusters, and a signature never changes when a filter does.

Triage (`docs/triage.md`) stays the per-experiment view: it counts observed patterns per arm. Clusters work across experiments and reuse triage's `pattern_loop` metric instead of detecting loops again.

## Where it lives

| Part | Path |
|---|---|
| Signatures, clustering (pure functions) | `clusters/` |
| CLI | `bifrost-bench clusters`, `cmd/bifrost-bench/clusters.go` |
| API | `GET /api/bench/clusters`, `GET /api/bench/clusters/{signature}`, `handlers/clusters.go` |
| Screen | `/clusters`, `ui/app/clusters/`, `ui/components/clusters/` |

## Which runs enter

Only runs the store's verified index returns: per slot the counted, sealed run (`store.FSStore.SealedRuns` with the original grade), each checked against its `seal.json` and the experiment's `seals.log`. A run whose seal does not verify, or that carries any other evidence problem, is left out and counted under `excluded`. Runs that pass count toward `runs` but form no cluster; `failures` is every other outcome.

Synthetic runs are left out by default (`-synthetic exclude`): a declared smoke experiment, a scripted control agent (by the manifest arm, or the run's own agent record), or a proxy ledger line labelled as answered by the scripted model. `-synthetic include` shows everything, `-synthetic only` only those. The leaderboard applies the same rule per experiment and config and also fingerprints ledgers older than the upstream label; clusters read only the label.

## The signature

Every run's signals are computed first. The signature then picks layers from them, most specific first.

| Layer | Source |
|---|---|
| `outcome` | `run.json` outcome: fail, timeout, budget, crash, refusal, integrity_violation, invalid_evaluator, infra_error, unknown |
| `failed_tests` | fail only: the grade's failed and missing required hidden tests, sorted, as a set |
| `state_checks` | fail only, state grades: the failed check ids |
| `failed_dimensions` | fail only, reward grades: the dimensions below 1 (dimensions are scored 0 to 1) |
| `answer_category` | fail only, answer grades: the task's answer category |
| `gates` | the fired gates in `gates.json`, sorted |
| `loop` | the repeated call of an observed `pattern_loop` (triage), normalized |
| `error` | the error fingerprint (below) |
| `provider` | the real provider's answer to the run's last model call when it failed: its HTTP status or `transport error`. Calls the fault proxy answered itself are the probe and are skipped |
| `last_tool` | the last tool call and whether it errored: from `tools.jsonl` when the task had proxied services, else the agent stream, else the gateway rows (which carry no outcome) |
| `condition` | the probe condition of the slot |

Which layers form the signature depends on the outcome, so a signature names a cause without splitting one cause into many clusters by incidental detail:

| Outcome | Layers |
|---|---|
| fail | outcome, gates, failed set, error |
| timeout, budget | outcome, gates, loop, error |
| crash, unknown | outcome, gates, error, provider |
| refusal | outcome, gates, error from the final message |
| integrity_violation | outcome, gates |
| invalid_evaluator | outcome, gates, error from the grader's reason |
| infra_error | outcome, gates, provider, error |

When the outcome's layers find nothing beyond the outcome and gates, the last tool call stands in when it errored, else the probe condition when it is not clean. The signature is `c_` and the first 16 hex characters of the sha256 over each chosen layer's kind and canonical value. It does not depend on which run, which order or which filter produced it, so `/clusters/{signature}` links stay valid. `TestSignatureStability` pins one value.

The title is built from the same layers: `fails test_limiter_window`, `timeout after repeated bash "pytest -q": stderr: segmentation fault`, `impossible_pass gate`, `crash: tool error: desk.refund 500`.

### Error fingerprint

The first source that has an error line wins:

1. for a refusal, the agent's final message (`claim.text`);
2. a part failure the engine recorded in `run.json`;
3. the last errored call in the tool proxy ledger, as `<server>.<tool> <status>` and its result's first line;
4. the agent stream's terminal error event: one with no agent tool call or model reply after it;
5. the gateway's terminal failed model reply;
6. the first line of `agent.stderr.txt` that contains an error word (error, exception, fatal, panic, failed, denied, refused, cannot, unable, invalid, not found, timeout, killed, unavailable, overloaded, ...);
7. for a crash, the final message; for an invalid evaluator, the grader's reason.

An error the agent carried on past (a startup warning logged as an error, a failed command it then fixed) is not how the run ended and is not used.

Normalization, per line: lower case; timestamps, uuids, hex strings, long ids and key-shaped tokens (anything 16 characters or longer mixing letters and digits), file paths with their `:line` suffix, `line N` references and every other number become placeholders (`<ts>`, `<uuid>`, `<hex>`, `<id>`, `<path>`, `<n>`); whitespace collapses; lines clip at 160 characters. A standalone three-digit number from 100 to 599 is kept, because it is almost always an HTTP status and `500` versus `404` is what a reader needs. The first line still holding two letters outside the placeholders is the stable line; for a Python traceback the last such line (the exception) is used instead of the header.

The agent's stderr is read by the CLI and server from the run folder. It is never served as a file; a cluster shows only its normalized first error line, as inert text.

## The cluster

| Field | Meaning |
|---|---|
| `count`, `share` | runs, and their share of all failures in scope |
| `tasks`, `agents`, `config_hashes`, `conditions`, `experiments` | what the runs touch |
| `first_seen`, `last_seen` | run start times |
| `examples` | the 3 most recent run ids |
| `scope` | `single_run`; `agent_and_task` (one config, one task); `agent` (one config only: agent-specific); `task` (one task only: task-specific, maybe a problem with the task itself); `systemic` |
| `after_config_change` | every run shares one config id and hash, an older hash of the same config id ran before that hash was first seen and ran at least one of the cluster's tasks, and no run of another hash has this signature |
| `related` | clusters with the same failed set and a different signature (usually a different error fingerprint), with what differs. Listed side by side, never merged |
| `error_text` | the normalized error line, when the error is a layer |

Clusters do not cross-reference suite health. For a task-specific cluster, check the task's admission record and health on its suite page.

## Hidden test names

The suite pages count hidden tests and never name them. The run page names the failed hidden tests of a sealed run (from its `grade.json`, served by `GET /runs/{id}`). Clusters follow the run page's rule: they name only failed hidden tests that sealed runs' grades already name, and every cluster links to those runs. Nothing is read from a suite's hidden test files.

## Use it

```
bifrost-bench clusters                         # every live failure, largest cluster first
bifrost-bench clusters -suite coding-core -since 7d -min 2
bifrost-bench clusters -agent codex -exp e_batch_frameworks_9z0h,e_batch_codewins_f8f7
bifrost-bench clusters -synthetic only -json
bifrost-bench clusters c_a5e4c40a251f8068      # one cluster with its layers, related clusters and runs
```

`-since` takes an RFC3339 time, a date (`2026-11-01`) or an age (`24h`, `7d`). `-agent` takes a config id or a config hash prefix of 6 or more characters. `-exp` repeats or takes a comma list. The API takes the same filters as query parameters (`suite`, `agent`, `exp`, `since`, `min`, `synthetic`); `min` hides small clusters from the list but never from `GET /clusters/{signature}`.

On the recorded bundle (`testdata/recorded`), the default view has 3 failures in 10 verified runs (the 8 smoke runs are excluded as synthetic): Codex and custom-python both fail the same 7 hidden rounding tests on t01 under model-outage, but Codex ends on its own stream's `turn failed: unexpected status 503` and custom-python on the gateway's failed reply, so they are two clusters listed as related; Codex's t05 failure is a third. Codex's `Model metadata ... not found` warning, logged as an error at startup, is not used because the run carried on past it.

## Removing it

Delete `clusters/`, `cmd/bifrost-bench/clusters.go` and its test, `handlers/clusters.go` and its test, `handlers/testdata/contract/Cluster*.keys`, `ui/app/clusters/`, `ui/components/clusters/` and this file, then the registry lines: the `NewClustersHandler` line in `server/server.go`, the two `/clusters` entries and the `clusters` import in `handlers/openapi.go`, the README row, and the UI's nav entry, route registration, types, API endpoints, fixtures and i18n keys.

## Honest limits

- **Grouped, not explained.** A cluster says these runs ended the same way. It does not say why; read the linked runs.
- **Text rules.** The fingerprint is the first stable line of free text. Two causes that print the same first line share a cluster, and one cause that prints differently (a different tool wording, a localized message) splits. Normalization can over-merge: two errors that differ only in a number or a path look the same.
- **Errors the agent labels itself.** An agent that ends on a benign message it logged as an error gets that message as its fingerprint.
- **Failed sets are exact.** Runs failing 6 of the same 7 tests are different clusters; `related` only links identical sets.
- **Config change** is read from run start times and config hashes within the filtered view. A filter that hides the older config's runs hides the change too.
- **Synthetic detection** reads the ledger's upstream label only; a dry run against the scripted model recorded before the label existed shows as live here (the leaderboard catches it by fingerprint).
