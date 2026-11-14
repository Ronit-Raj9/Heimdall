# Recorded bundle

A runs root holding sealed experiments that the CI gate (`../../ci/`, `../../docs/ci.md`) re-analyzes with `bifrost-bench check -exp <id>`. No agent, gateway, Docker or key is needed to replay it.

## `e_pilot_mixed_nc93`: REAL

Real agents, a real model and real Bifrost. This is the bundle the CI workflow replays.

| Field | Value |
|---|---|
| Agents | arm A: Codex CLI 0.169.0 (`agents/codex.yaml`); arm B: the custom-python template agent (`agents/custom-python.yaml`, no framework) |
| Model | `gpt-5.4-mini` on OpenAI, through stock Bifrost `v2.5.1` and the fault proxy (real upstream; the key lived only in the proxy) |
| Experiment file | `experiments/pilot-mixed.yaml`: one run per arm of clean (t02), stale-notes (t01), model-outage (t01), test-crash (t06) and empty-test-run (t05) |
| Runs | 10, all sealed. Arm A: pass on clean, stale-notes and test-crash; fail on model-outage and empty-test-run. Arm B: pass on clean, stale-notes, test-crash and empty-test-run; fail on model-outage |
| Spend | $0.172 for all 10 runs, from the bench's own cost ledger |
| Fault receipts | every probe armed and fired; the run nonce reached the model in Bifrost's request rows for stale-notes, test-crash and empty-test-run; under model-outage Codex retried until 30 of its 32 requests had been answered with 503 |
| Manifest hash | `be32a712e20c78445992042df1aa72b7f26f2fa5f622060d0480799c5df6a160`, frozen 2026-11-14T01:00:36Z |
| Verdict | `inconclusive`, action `no_decision`, policy `warn` (5 tasks, 1 run each: too few to decide anything, by design) |
| Size | 184 files, about 19 MB, most of it Bifrost's request rows (`gateway.requests.jsonl`) |

It was copied from `bench/runs/e_pilot_mixed_nc93` byte for byte (`cp -a`) and made owner-writable; `GOWORK=off go test ./ci/` verifies its seals. Before the copy every file was scanned for the values in `deploy/.env` (provider key, setup token, Postgres password, fault proxy token) and for `sk-proj-`, `Bearer sk-` and Bifrost virtual keys (`sk-bf-`): none were found.

The larger real batch (240 runs: Codex against custom-python under all three PS2 failure families, a seeded regression, and the code-wins experiment) is too big for git (about 560 MB); its reports are in `../../docs/results/`.

## `e_smoke_sdoj`: SYNTHETIC

**This experiment is synthetic. Its numbers are not findings about any agent or model.**

| Field | Value |
|---|---|
| Agent | the scripted toy agent (`agents/toy.yaml`, adapter `scripted`) in both arms; the arms differ only in the notes file (`experiments/fixtures/codewins.md` in arm B) |
| Model | the fault proxy's scripted Anthropic model (no provider key was set) |
| Gateway | real, stock Bifrost `v2.5.1` (`maximhq/bifrost:v2.5.1`), every call logged; manifest gateway config sha256 `ab0147380a09...` |
| Experiment file | `experiments/smoke.yaml`: tasks `t01`, `t02` of `coding-core@1.0.0`, conditions `clean` and `model-outage`, 1 trial, arms A and B |
| Runs | 8, all sealed: 4 clean (pass), 4 model-outage (fail, as the probe intends) |
| Manifest hash | `20d282759cb94d544fc6018d0dbf318e2d46917d090b4d81cd12f91c6c0bbe29`, frozen 2026-11-13T23:28:48Z |
| Verdict | `inconclusive`, action `no_decision` (2 tasks, 0 discordant) |
| Size | 152 files, 842,332 bytes |

### How it was produced

1. `make up` started Postgres, Bifrost v2.5.1 and the fault proxy, with no provider key, so the proxy served its scripted model.
2. `bifrost-bench run experiments/smoke.yaml` (what `make smoke` runs) planned, froze, ran, graded and sealed the 8 runs into `bench/runs/e_smoke_sdoj`, then wrote the first analysis.
3. On 2026-11-14 the folder was copied here byte for byte (`cp -a`), every file and seal kept, and made owner-writable: the store seals runs read-only, git does not keep folder modes, and seals hash file bytes, not modes.

The `analysis/` folder keeps the three analyses written before the copy. `a_dt5a332t` is labelled mode `live` (the mode the bench was configured with when the run finished); `a_tzm14kdx` and `a_0nul9v0w` are labelled `synthetic`. The data is synthetic in all three. The CI gate writes its own analysis into a temp copy, never here.

### Checks done on the copy

- Seals: `GOWORK=off go test ./ci/` verifies the `seals.log` chain and every run's seal with the store's `VerifyChain` and `Verify`, and proves a changed, added or removed file fails. The composite action runs it before every replay.
- Secrets: no match for `sk-`, `Bearer `, `BIFROST_SETUP_TOKEN`, `Authorization`, `api_key` or any value from `deploy/.env` (setup token, Postgres password, fault proxy token, provider keys). The only `x-api-key` matches are the header name in the toy agent's source, which sends a placeholder variable. Key ids in the gateway rows are Bifrost key-config UUIDs and slot names (`fp-slot-1`), not key values. The bench never writes keys to run folders.

## Keeping the bundle intact

- `.gitignore` here re-includes `runs/` and `*.log`, which `bench/.gitignore` and the repo `.gitignore` would otherwise drop, so the sealed run folders and `seals.log` are committed.
- `.gitattributes` turns off line-ending conversion: seals are sha256 over raw bytes.
- Never edit a file in a sealed run. A hand-edited bundle fails `go test ./ci/` and the CI gate.

## Next

A real recorded run (a real agent and a real provider model through the same stack) will join this bundle as a second experiment folder, with its own section here. The CI job then points `exp-id` at it and sets `data-mode: recorded`.
