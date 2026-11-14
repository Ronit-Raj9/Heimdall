# Recorded bundle

A runs root holding sealed experiments that the CI gate (`../../ci/`, `../../docs/ci.md`) re-analyzes with `bifrost-bench check -exp <id>`. No agent, gateway, Docker or key is needed to replay it.

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
