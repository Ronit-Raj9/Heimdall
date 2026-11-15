# Results index

The results index is a derived, searchable table of results across every experiment in the run folder: one row per verified sealed run and one row per analysis. It exists so trends and search do not have to walk and re-verify every run folder on each request.

**Sealed run folders stay the only source of truth.** The index is a cache of facts read from them. Delete it whenever you like; `bifrost-bench index rebuild` writes it again from the folders.

**Nothing that decides reads it.** The verdict, the leaderboard, `regate` and `release` keep reading the sealed files and verifying their seals. The index serves trends (`docs/trends.md`), search and `index query` only, so a stale or damaged index can mislead a chart but never a release.

## Where it lives

| Part | Path |
|---|---|
| Index | `resultsindex/` (`index.go` file format and `Reader`, `build.go` derivation and verification, `query.go` filters) |
| File | `<runs root>/.index/results.jsonl` (git-ignored with the runs root; the leading dot keeps it out of the experiment list) |
| CLI | `bifrost-bench index rebuild\|status\|query` (`cmd/bifrost-bench/index.go`) |
| Readers | `trends/` and `GET /api/bench/trends` |

## Format: JSON lines, not SQLite

The index is one JSON-lines file with an in-memory loader. SQLite through a pure-Go driver was the first choice (the binary is built with `CGO_ENABLED=0`, so it would have to be `modernc.org/sqlite`), but the module ships no SQLite driver and the bench adds no third-party dependency without the lead (`AGENTS.md`). The readers use the `resultsindex.Reader` interface (`Runs(filter)`, `Analyses(exp)`, `Rejected()`, `Status()`), which a SQLite store could implement later without changing them.

The first line is a meta line with the format version. A file of another version reads as empty, so an older binary never misreads a newer layout; the next write replaces it. Each write goes to a temp file and is renamed into place, so a reader in another process sees the old file or the new one, never half of one. Two processes writing at once race only to the last rename, and the loser's rows come back on the next update.

## Rows

**Run row** (counted runs only; compatibility runs never enter results): experiment and its creation time, run id, `suite` as id@version, task, arm, agent config id, config hash, model, condition, trial, outcome, `score` (the run's Y), `success` (verified success 0 or 1, null when the outcome does not score), `metrics` (every observed metric value), `cost_usd`, `wall_time_s`, started time, seal hash, `synthetic`, `grade_source` (always `original`), `not_repeatable` (the experiment called real external hosts) and any evidence problem other than a seal failure. A value that was not observed is null or absent, never zero. One exception follows the release policy and the leaderboard: `false_completion` on a run whose verifier passed is not applicable and is stored as 0, because such a run is never a false completion.

**Analysis row**: experiment, analysis id, verdict decision, action, policy decision (allow, warn, block, or empty with no policy), grade source, created time.

**Rejected entry**: a sealed run whose files do not match its seal, or a whole experiment whose runs could not be read (its manifest is unreadable, or a run's canonical record does not match). It names the reason and is never a row.

**Synthetic** follows the leaderboard (`leaderboard/synthetic.go`): a smoke experiment, a scripted control agent, or any run of the arm whose proxy ledger shows the scripted model makes every run of that arm in that experiment synthetic.

## Verification

Every row is read through `store.SealedRuns`, the store's one verified path: each run is compared with its `seal.json` and with the hash `seals.log` recorded, through the store's verification cache. A run that does not verify becomes a rejected entry. The index never parses a file of a run that failed its seal.

| Operation | What it verifies |
|---|---|
| `index rebuild` | Every experiment, every counted sealed run, from scratch |
| Incremental update after a run seals | The one experiment (`run` and `check` start it in the background on each seal event; a burst of seals becomes one update) |
| Sync (before `index query` and each trends request) | Experiments whose `seals.log` size and time or analysis folders changed since they were indexed; unchanged experiments keep their rows; removed experiments are dropped; a missing file is built in full |

Sync trusts rows it wrote earlier for an experiment whose `seals.log` did not change. If a sealed file may have been altered after indexing, run `index rebuild`, which re-verifies everything. A failed incremental update is only logged: the folders are intact, and the next sync or rebuild catches up.

## Commands

```
bifrost-bench index rebuild [-config deploy/bench.yaml]
bifrost-bench index status  [-config deploy/bench.yaml]
bifrost-bench index query   [-config deploy/bench.yaml] [-format table|json] [-limit n] [-analyses] [-no-sync] [key=value ...]
```

`rebuild` prints the status and every rejected entry. `status` reads the file as it is (no sync). `query` syncs first unless `-no-sync`, then prints run rows (or analysis rows with `-analyses`, filtered by `experiment=` only) as a table or JSON.

Query keys match exactly: `experiment`, `run_id`, `suite`, `task`, `arm`, `agent`, `model`, `condition`, `outcome`, `config_hash`; `synthetic=include|exclude|only` (default include); `since` and `until` bound the run's start time, as RFC 3339 or `YYYY-MM-DD` (`until` is exclusive).

```
bifrost-bench index query agent=codex condition=clean synthetic=exclude since=2026-11-01
bifrost-bench index query -format json outcome=timeout
bifrost-bench index query -analyses experiment=e_nightly_1115
```

None of these need the gateway or Docker. Exit codes: 0 ok, 4 error, 64 usage.
