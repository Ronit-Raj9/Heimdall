# Releases and evidence packs

A release record says which agent config was released, who approved it, and which analysis and sealed runs it rests on. Each release also gets an evidence pack, a zip you can hand to someone who was not in the room.

The release log is hash-chained. Each record carries the hash of the record before it and a hash of itself, so editing, removing or reordering a past record shows up when the chain is checked. That is all the chain proves: the log has not changed since it was written. It is not a cryptographic signature, it does not prove who wrote a line, and it is not a compliance certification.

## Where it lives

The releases root is `$BENCH_RELEASES_ROOT`, or `<bench root>/releases` when that is unset. It must not sit inside the runs root, because the run store reads every folder there as an experiment; `release.New` refuses such a root.

```
releases/
  releases.jsonl              one schemas.ReleaseRecord per line, oldest first
  rel_<id>/evidence.zip       the evidence pack of that release
```

Records are appended with `O_APPEND`, one write per line, then `fsync`, under an exclusive `flock` on `releases.jsonl`. The lock is held from reading the chain tail until the line is written, so two releases (from the CLI and the server at once) cannot fork the chain.

## The release rule

`release.Log.Create` loads the manifest and the analysis: the one named by `analysis_id`, or else the analysis under `<runs>/<exp>/analysis/` with the latest `created_at`. Then:

| Check | Fails as |
|---|---|
| `approver` is set | refusal |
| the analysis was computed on this manifest (`verdict.manifest_hash`) | refusal, not overridable |
| the experiment has counted sealed runs | refusal, not overridable |
| verdict action is `ship` | refusal unless `override` gives a reason |
| policy decision, when a policy was evaluated, is not `block` | refusal unless `override` gives a reason |
| when the experiment froze a policy, the analysis evaluated that policy (same `policy_hash`), not `check -policy none` or another file | refusal unless `override` gives a reason |
| `override` is set only when something needs overriding | refusal |
| every counted run's seal verifies (`Store.Verify`) | refusal, not overridable |
| the existing log's chain verifies | refusal, not overridable |

The policy result is read from the analysis (`Analysis.Policy`), or from `policy.json` beside `verdict.json` when the analysis has none. No policy result means no policy was evaluated; for an experiment without a frozen policy only the action rule applies.

An override is recorded in the record and shown in red on the timeline. It never bypasses a seal mismatch or a broken chain.

## The record

Every field is `schemas.ReleaseRecord` (`schemas/policy.go`):

- `arm` is the verdict's arm B (normally `B`); `agent_config_id` and `config_hash` come from that arm in the manifest.
- `seals_digest` is the sha256 of the counted runs' seal hashes, sorted, each followed by a newline. It binds the release to exactly those sealed runs.
- `evidence_path` is relative to the releases root; `evidence_sha256` is the sha256 of the zip.
- `prev_hash` is the previous record's `hash` (empty for the first). `hash` is the sha256 of the record's canonical JSON (`utils.CanonicalJSON`) with `hash` set to `""`.

A line must be exactly the canonical JSON of its record: an added field, a reformatted line or an edited value fails the check.

## Evidence pack

`<releases root>/<release_id>/evidence.zip`, in this order:

| File | What it is |
|---|---|
| `README.txt` | what each file is and how to verify it |
| `release.json` | the record as it was before the pack was hashed: `evidence_sha256` and `hash` are empty; the log line carries both |
| `manifest.json` | the frozen manifest |
| `analysis/verdict.json`, `analysis/report.md` | the cited analysis and its report |
| `analysis/policy.json` | the policy result, when one was evaluated |
| `seals.log` | the experiment's seal chain |
| `runs/<run_id>/seal.json` and every path listed by that seal | each counted run's complete sealed folder, copied byte for byte; this includes events, cost rows, tool ledgers, gateway request evidence, agent streams and stderr when present |
| `runs/<run_id>/regrade.grade.json` | the regrade grade, when the analysis used a regrade |

Every entry has the same fixed time (1980-01-01 UTC) and mode, and runs are in run id order, so the same record and files always give the same zip bytes. The pack is the audit copy: a file is included because it is named in that run's `seal.json`, rather than because it happens to be one of a fixed set of summary files.

To check a pack by hand: `sha256sum runs/<run_id>/seal.json` must appear on that run's line in `seals.log`; every path under `seal.json.files` must be present below `runs/<run_id>/` and hash to its listed value; the sorted seal hashes, newline-terminated, must hash to `seals_digest`; and the zip's own sha256 must equal the record's `evidence_sha256` in `releases.jsonl`.

## CLI

```
bifrost-bench release create -exp <id> -approver <name> [-note text] [-override reason] [-analysis <id>]
bifrost-bench release list
bifrost-bench release verify
bifrost-bench release export <release_id> [-o path]
```

`create` exits 1 on a refusal and prints the reason. `list` prints newest first and the chain status. `verify` checks the chain and that every evidence pack still hashes to its record; it exits 1 when either fails. `export` prints the pack's path, or copies it to `-o`, after checking its hash.

## API

| Route | |
|---|---|
| `POST /api/bench/releases` | write token. Body `schemas.ReleaseRequest`; 201 with the record; 409 with the reason on a refusal; 400 on a malformed request or a missing approver |
| `GET /api/bench/releases` | `schemas.ReleaseList`: records in log order (oldest first) and `chain_ok` |
| `GET /api/bench/releases/{id}` | one record |
| `GET /api/bench/releases/{id}/evidence` | the zip, `application/zip`, as an attachment; an error if it no longer matches its record |

Raw run evidence remains excluded from the ordinary `/runs/{id}/files/{name}` allowlist. An explicit `GET /api/bench/runs/{id}/evidence/{name}` download serves `gateway.raw.jsonl`, the redacted `gateway.requests.jsonl`, `proxy.ledger.jsonl`, `tools.jsonl`, `agent.stream*.jsonl` and `agent.stderr*.txt` only after the run is sealed and its seal verifies. The response is an attachment with `application/octet-stream`, `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`; unknown names, unsealed runs and seal mismatches are refused.

The handler depends on the small `handlers.ReleaseLog` interface; `cmd/bifrost-bench` wires in `release.Log`. A refusal is any error with a `ReleaseRefused() bool` method, so `handlers` does not import `release`.

## UI

`/releases` shows the timeline, newest first, with the chain status of the whole log (from `chain_ok`) and of each link (`prev_hash` against the record before it), and a Release panel. The panel shows the picked experiment's verdict action and policy decision and sends the analysis id it shows, so the release cites the analysis on screen. The Release button is enabled only when the action is `ship` and the policy does not block; otherwise an explicit "Override with a reason" checkbox is the only way through.

## Limits

- The chain detects edits after the fact. Anyone who can write `releases.jsonl` can also rewrite the whole chain from some line on; keep a copy of the latest `hash` somewhere else (a ticket, a commit message) if that matters.
- Approver names are what the caller typed. The write token says the caller may write, not who they are.
- Opening the verdict page or the Release panel runs an analysis, which writes a new analysis folder. A release cites the analysis id it was given.
