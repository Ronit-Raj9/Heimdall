# Evidence storage

Sealed run folders are the product's evidence (`docs/evidence-integrity.md`). On one laptop they live on one disk; in a hosted service they must survive the loss of that disk and come back with proof that nothing changed. This page covers the second copy: the mirror, how it is written, how it is read back, how old experiments leave local disk, and how the operational database is backed up.

**The run folder stays the evidence.** The mirror is a byte-for-byte copy of an experiment's immutable files plus one listing of their sha256s. Nothing here writes into a sealed folder, and nothing reads a result from the mirror without first rebuilding the folder and checking every seal and the `seals.log` chain with the store's own code.

## Where it lives

| Part | Path |
|---|---|
| Package | `evidencestore/`: `blob.go` (the object storage interface), `dir.go` (folder backend), `collect.go` (what is mirrored, after which checks), `mirror.go`, `restore.go`, `verify.go`, `archive.go` (retention and stubs), `backup.go` (database), `service.go` (background mirror) |
| S3 client | `evidencestore/s3blob/`: SigV4 over `net/http`, no SDK |
| Config section | `evidencestore/evconfig/` (`storage:` in `deploy/bench.yaml`, carried by `lib.Config.Storage`) |
| CLI | `bifrost-bench evidence mirror\|verify\|restore\|backup\|archive\|list\|fetch-backup` (`cmd/bifrost-bench/evidence.go`) |
| Server hook | `startEvidenceMirror` in `serve`, `evidenceOnSeal(orgID, expID)` for the job runner |

## Configuration

```yaml
storage:
  root: runs                  # hosted: per-org folders under runs/orgs/<org_id>/ (empty: runs_root)
  # Either a folder mirror (a second disk, a network share) ...
  mirror_dir: /mnt/backup/heimdall
  # ... or an S3-compatible bucket.
  s3:
    endpoint_env: HEIMDALL_S3_ENDPOINT     # https://host[:port]; empty value means AWS S3
    bucket: heimdall-evidence
    region: us-east-1                      # R2: auto; B2: the bucket's region, e.g. us-west-004
    prefix: ""                             # optional key prefix inside the bucket
    access_key_env: HEIMDALL_S3_ACCESS_KEY
    secret_key_env: HEIMDALL_S3_SECRET_KEY
    session_token_env: ""                  # temporary credentials only
    addressing: auto                       # auto | path | virtual
    checksum_sha256: false                 # also send x-amz-checksum-sha256 (AWS, MinIO, R2)
  sweep_interval_m: 15                     # server sweeper; negative switches it off
  concurrency: 4                           # parallel transfers
```

Secrets are named by environment variable, never written in the file. Create the bucket before the first pass (the mirror never creates buckets). The server keeps the mirror off, with one log line, while `storage.s3.bucket` is set but the access and secret key variables are empty, so a hosted template can name the bucket before credentials exist. Every CLI command also takes `-mirror file:///dir`, `-mirror /dir` or `-mirror s3://bucket/prefix` to override the section (credentials still come from the environment).

| Store | endpoint | region | addressing |
|---|---|---|---|
| AWS S3 | empty | the bucket's region | auto (virtual-host) |
| MinIO | `http://minio:9000` | `us-east-1` | auto (path) |
| Cloudflare R2 | `https://<account>.r2.cloudflarestorage.com` | `auto` | auto (path) |
| Backblaze B2 | `https://s3.<region>.backblazeb2.com` | e.g. `us-west-004` | auto (path) or virtual |

`auto` picks virtual-host addressing only for `*.amazonaws.com` with a DNS-safe bucket name without dots, path style everywhere else.

## Layout in the mirror

```
<org_id>/<exp_id>/manifest.json
<org_id>/<exp_id>/seals.log
<org_id>/<exp_id>/slots/<slot_id>/slot.json
<org_id>/<exp_id>/slots/<slot_id>/runs/<run_id>/...      sealed runs only
<org_id>/<exp_id>/regrades/... analysis/... reviews/... reviews.log
<org_id>/<exp_id>/.mirror.json                           the listing, written last
_backups/db/heimdall-<UTC time>.sqlite | .pgdump         database backups
_backups/db/heimdall-<UTC time>.<ext>.json               their records (size, sha256, check)
```

Local mode's organization is `local` (the runs root itself); hosted organizations are `runs/orgs/<org_id>/`. Paths inside an experiment are the same as on disk (DESIGN.md 16.1).

**The listing** (`.mirror.json`, format `heimdall-evidence-mirror/1`) names every mirrored file with its size and sha256, the manifest hash, the number of lines of the mirrored `seals.log` and its last seal hash, and a generation that goes up by one each time the listing changes. It is written after every file it names, so it is the commit point: a file is in the mirror when the listing names it, and restore reads only what the listing names. Objects under the prefix that no listing names (left by an interrupted pass) are harmless and reported by `verify` as unlisted.

## What is mirrored

| Mirrored | Not mirrored |
|---|---|
| `manifest.json`, after checking that its `hash` field is the sha256 of its canonical JSON (the rule `engine.ManifestHash` applies) | `slots/*/lease.json`, `lease.lock`: operational state |
| the append-only logs `seals.log`, `reviews.log` and `resumes.jsonl`, as read once at the start of the pass, complete lines only | run folders not in that `seals.log`: in flight or never sealed (reported as pending, picked up by a later pass) |
| every `slot.json` | outside sealed runs: names starting with a dot (temporary files), non-regular files and a stray `archived.json` (reported as skipped; a sealed run's own dot files are mirrored) |
| every run folder named in that `seals.log`, after `store.Verify` passes for the run, `store.VerifyChain` passes for the experiment, and its seal names the experiment's manifest hash | |
| regrades, analyses, reviews and anything else the store writes | |

Restore accepts from a listing only paths a pass can select, so a lease, a stub or a dot file planted in the bucket is refused.

## When it runs

- **On seal (the hook).** The server's job runner calls `evidenceOnSeal(orgID, expID)` when a run seals (see "Hooks for other areas"). It never blocks: the experiment is queued, and after a short debounce (5 s, so a burst of seals is one pass) one pass runs.
- **The sweeper.** The server sweeps every organization 30 s after start and then every `sweep_interval_m` minutes, so a seal the hook missed (a crash, a CLI run, a server that was down) still reaches the mirror. A pass or sweep is cut off after two hours and retried by the next sweep. A failing experiment or organization is logged and the sweep goes on with the rest.
- **By hand or from cron.** `bifrost-bench evidence mirror [-org id] [-exp id] [-repair]`, or `evidence backup` (below). Exit 4 when any experiment or organization failed.

A normal pass trusts the listing for files it already names. `-repair` also checks each of them with `Head` and sends again any whose object is gone or differs (a bucket that lost an object); archive always runs a pass with repair.

Passes over one experiment are serialized inside a process (a mutex) and across processes (a lock file under `<root>/.evidence/locks/`), so the server and a cron job never interleave.

## What a mirror pass checks and refuses

A pass refuses the whole experiment, sends nothing and leaves the mirror's last good copy in place (`ErrRefused`, exit 4) when:

- any logged run fails `store.Verify` (a file changed, added or removed; the error names the first file), or `store.VerifyChain` fails, or a run in `seals.log` has no folder, or a plain file sits directly under a `runs/` folder;
- a sealed file's name can not be an object key (longer than S3's 1024 bytes with its prefix, or a reserved segment);
- `manifest.json` does not hash to its own `hash` field, or a seal names another manifest;
- a file the mirror already holds changed locally (sealed runs, analyses, slot files and the manifest never change once written);
- an append-only log is not the mirrored log plus new lines: the pass checks that the sha256 of the local log's first N bytes equals the mirrored log's sha256, where N is the mirrored size. A rewritten or shortened chain is refused, so the mirror is an outside anchor for the chain (see "What it proves" below);
- a file changes while it is being sent: every object is put with the sha256 the checks just read, and the backend refuses bytes that hash to something else.

A pass is **idempotent**: with nothing new it sends nothing and does not rewrite the listing. It is **resumable**: a file an interrupted pass already sent is found with `Head` (same size, same recorded sha256) and not sent again; the listing written at the end covers everything. The mirror never forgets: a file the mirror holds that disappeared locally stays in the listing and is reported by `verify` as extra.

## Restore

```
bifrost-bench evidence restore -org <org_id> -exp <exp_id> [-to <dir>]
bifrost-bench evidence restore -org <org_id> -all [-to <dir>]   # every mirrored experiment of the org absent locally
bifrost-bench evidence restore -all [-to <dir>]                 # every organization the mirror holds (after a disk loss)
```

Organization folders that do not exist yet are created, so `-all` works on an empty runs root. With `-to` and no `-org`, each organization goes to `<dir>/<org_id>`.

1. The listing is read and checked (format, organization, experiment, sorted unique paths, no path that leaves the folder).
2. If the experiment is archived (a stub in the organization's folder, also when restoring elsewhere with `-to`), the listing's sha256, seal count and last seal must equal what the stub recorded when the experiment was archived; a mirror rewritten after archiving is refused.
3. Every listed file is downloaded into `<root>/.evidence/staging/<exp_id>.<random>/` through a reader that hashes the bytes and fails at the end of the body when the size or sha256 differs from the listing (a changed object or a transfer error). For an append-only log exactly the listed number of bytes is read: a later pass that sent a longer log and failed before its listing leaves an object that starts with the listed bytes.
4. The staged copy is checked as the analysis would check it: manifest hash, `seals.log` length and tail against the listing, `store.VerifyChain`, `store.Verify` for every logged run, no run folder outside the log, every seal naming the manifest. Review problems are reported as warnings.
5. Sealed run folders get their sealed modes back (files 0444, folders 0555), then the staged folder is renamed into place (a stub is moved aside first and put back if the rename fails).

Any mismatch refuses the restore: nothing moves, the staging folder is removed and the error names the file or the check. A live experiment folder is never replaced; compare it with `evidence verify`. `-to <dir>` restores into any other folder and leaves the live store alone: that is an auditor's verified copy. A running server sees a restored experiment after its index is rebuilt (restart, or the index rebuild job).

## Verify

```
bifrost-bench evidence verify -org <org_id> [-exp <exp_id>|all] [-deep] [-json]
```

| Field | Meaning |
|---|---|
| missing | an immutable local file the mirror does not hold (not mirrored yet, new log lines), or any listed object that is gone, whether or not the local file still exists |
| extra | a mirrored file the local folder no longer has |
| mismatched | a file on both sides whose size or sha256 differs, or an object whose stored size, recorded sha256 or (with `-deep`) bytes differ from the listing |
| unlisted | objects no listing names (leftovers of an interrupted pass) |
| local error | the local folder fails its own seal or chain check, its archive stub does not match the mirror, or there is no local folder at all (every mirrored file is then extra) |
| skipped | local entries a pass never mirrors (dot names, non-regular files outside sealed runs) |

Exit 0 when nothing is missing or mismatched and the local folder verifies (extra and unlisted files are reported but allowed), 1 otherwise. Without `-deep`, objects are compared by their recorded sha256 (`x-amz-meta-sha256` on S3, the file's own hash on a folder mirror); `-deep` downloads and hashes every object.

## Retention and archive

Each organization can set `retention.archive_after_days` (the `settings` table, owned by the database area). `bifrost-bench evidence archive` reads it per organization (or takes `-days n` for all of them) and archives every experiment whose last activity is older: the newest of the manifest's creation time and the modification times of `seals.log`, `reviews.log`, every analysis, regrade and review folder, every slot's `runs/` folder and `lease.json`. `-dry-run` reports what it would do; `-exp id` archives one experiment now.

Archiving one experiment, in order:

1. a refusal while a slot holds a live lease (`lease.json` with a heartbeat in the last hour);
2. a mirror pass with repair, with every check above;
3. a refusal if any run folder is not sealed ("resume or finish the experiment first") or any entry would not be mirrored (skipped);
4. a verification of the mirror against the local folder; anything missing or mismatched refuses;
5. a restore drill: the whole listing is downloaded into a staging folder and checked exactly as `restore` checks it, then discarded;
6. the stub is written into a staging folder, the experiment folder is renamed into `<root>/.evidence/trash/<exp_id>.<random>`, and the trashed folder is compared with the listing once more: a file written after the mirror pass (an analysis, a review, a new seal) puts the folder back and refuses, and the next attempt mirrors it first;
7. the stub folder is renamed into place and the trash deleted.

**Nothing is deleted unless the mirror copy has been restored and verified.** A crash is finished or undone by the next archive, restore or sweep, from what stands at `<root>/<exp_id>`: nothing there, and the trashed folder is moved back; a valid stub, and the trash is deleted (the stub goes in only after every check); a restored live folder while the trash holds only a stub, and the trash is deleted; anything else (an empty or half-written folder), and both are kept and an error is logged for a manual check.

**The stub** (`heimdall-archive-stub/1`) is the only file left in the folder. Because the folder has no `manifest.json`, the store does not list the experiment and no verdict, leaderboard or release reads it; because the folder exists, the id can not be reused. It holds the experiment's name, suite, arms, creation, last activity and archive times, manifest hash, run, file and byte counts, the mirror location and the listing's key, sha256, generation and last seal. `evidencestore.ListArchived(root)` returns the stubs of a store root, for the UI to list beside live experiments with a restore action; `evidence restore` brings one back.

## Database backup

The database holds operational state (users, organizations, memberships, tokens, jobs, runners, audit) and the rebuildable index, never evidence. `evidence backup` uses the database package's own backup (`db.DB.Backup`, also behind `bifrost-bench db backup`): SQLite `VACUUM INTO`, a consistent copy taken in one read transaction while writers continue; Postgres `pg_dump --format=custom` with the password passed in the environment. Then it checks the file: the SQLite copy is opened read-only and must pass `PRAGMA integrity_check`; the Postgres dump must be readable by `pg_restore --list` (when installed). The file (mode 0600) and its record (size, sha256, check) are uploaded to `_backups/db/`. The file holds password and token hashes: keep the bucket private. `evidence list` shows the backups; `evidence fetch-backup -name <file> -out <dir>` downloads one and checks its sha256.

## Runbook

**Nightly** (cron or a systemd timer on the server host):

```
bifrost-bench evidence backup  -config deploy/bench.yaml     # database backup + mirror sweep + upload
bifrost-bench evidence archive -config deploy/bench.yaml     # per-org retention, only after a verified mirror
bifrost-bench evidence verify  -config deploy/bench.yaml -org <org_id> -exp all   # optional; -deep weekly
bifrost-bench evidence mirror  -config deploy/bench.yaml -repair                   # weekly: re-send objects the bucket lost
```

`backup` exits 4 when the database backup failed (the evidence sweep still runs) or any experiment was refused (a tampered or broken folder: investigate with `evidence verify` and `docs/evidence-integrity.md`; the mirror keeps its last good copy). Bucket settings worth turning on: versioning (every listing generation stays readable), object lock or a retention policy for an outside anchor, and a lifecycle rule `AbortIncompleteMultipartUpload` (an interrupted multipart upload otherwise lingers).

**After losing the server's disk:**

1. Install Heimdall and restore `deploy/bench.yaml` and the environment (`.env`).
2. `bifrost-bench evidence list -config deploy/bench.yaml` to see the backups and mirrored experiments.
3. Database: `evidence fetch-backup -name heimdall-<time>.sqlite -out <runs_root>/.heimdall/` and rename it to `heimdall.db`; or for Postgres `pg_restore --clean --if-exists --no-owner -d "$HEIMDALL_DATABASE_URL" heimdall-<time>.pgdump`. Then `bifrost-bench db migrate`.
4. Evidence: `bifrost-bench evidence restore -all` brings back every organization the mirror holds (`-org <org_id> -all` for one). Every experiment is verified seal by seal before it moves into place.
5. Start the server; it rebuilds its index from the restored folders.

## What an auditor gets

- The mirror: plain files under `<org_id>/<exp_id>/`, readable with any S3 tool or file browser, in the run folder layout of DESIGN.md 16.1.
- The listing: one indented JSON file per experiment with every file's sha256, the seal count and the last seal hash. Its own sha256 (printed by `mirror`, `verify` and `restore`, and recorded in an archive stub) is a short fingerprint of the whole experiment to write down or publish.
- A verified local copy without touching the live store: `bifrost-bench evidence restore -org <org> -exp <exp> -to <dir>` runs every seal and chain check before anything lands, and the copy can then be analysed with the bench's own commands.

## What it proves and what it does not

A restore that succeeds proves that the files are the ones the listing names and that they pass the same seal and chain checks as at sealing time. The mirror adds what the seals alone lack (`docs/evidence-integrity.md`): an anchor held somewhere else. A local `seals.log` rewritten after mirroring is refused by the next pass, and an archived experiment is restored only from the exact listing its stub recorded.

It does not prove anything against someone who can write both the local folders and the bucket: they can rewrite a run, its seal, the chain and the listing consistently. Bucket versioning and object lock, or publishing listing hashes somewhere the operator can not edit, close that gap. It also does not make a wrong grade right; it keeps what was sealed.

## The S3 client

`evidencestore/s3blob` is a small SigV4 client over `net/http` (no SDK), checked against the AWS-documented signature examples and a real MinIO. Every write sends `Content-MD5` and the real sha256 of the body as the signed payload hash (never `UNSIGNED-PAYLOAD`), so the store itself rejects a body that changed in transit; it also stores the sha256 as `x-amz-meta-sha256` (what `Head` and `verify` read) and, with `checksum_sha256`, `x-amz-checksum-sha256`. Objects of 64 MiB and more go up as multipart uploads (16 MiB parts, each with its own MD5 and payload hash; aborted on failure). Network errors, 5xx, 429, `SlowDown`, `RequestTimeout` and `InternalError` are retried with exponential backoff and jitter (5 attempts); a skewed clock is corrected from the server's `Date` header. Redirects are not followed (a signed request is never resent to another host) and no credential appears in an error or a log line.

## Hooks for other areas

| Area | Hook |
|---|---|
| Jobs (P4) | call `evidenceOnSeal(orgID, m.ExpID)` where the job runner sees `engine.EventRunSealed` (`servejobs.go`); without it the sweeper still mirrors within `sweep_interval_m` |
| Tenancy (P3) | call `startEvidenceMirror(ctx, a)` in the hosted serve path as `serve` does in local mode; a registry-backed `evidencestore.OrgSource` can replace `RunsRootOrgs` to share the stores' verification caches |
| Database (P1) | `retention.archive_after_days` in `settings`; the index rebuild can list archived experiments with `evidencestore.ListArchived(root)` |
| UI (U1) | list archived experiments from the stubs and offer restore (a job or `evidence restore`) |

## Tests

`go test ./evidencestore/...` covers the folder backend and the verifying reader; mirror and restore round trips on synthetic and recorded experiments; idempotent, incremental, resumed and repaired passes; refusal of tampered runs, broken chains, rewritten append-only logs (`seals.log`, `resumes.jsonl`), files directly under `runs/` and names that can not be keys; five tamper cases refused on restore (changed object, corruption in transit, object and listing rewritten together, a `seals.log` line removed, a forged seal and log line); a log object longer than its listing; verify reports, including lost objects and mirror-only experiments; archive and in-place restore, archive refusals (unsealed run, live lease, skipped entry, lost object, a file written during the archive), a mirror changed after archiving (in place and with `-to`); retention; crash recovery, including the cases where both folders are kept; sweeps across organizations, past a failing one; the background service; concurrent passes and the lock across processes; listing validation; the database backup (SQLite always, Postgres with `HEIMDALL_TEST_PG_DSN`). `cmd/bifrost-bench/evidence_test.go` drives every subcommand, a disaster recovery of a local and a hosted organization from an empty runs folder, and the server hook. With `-tags integration` the S3 client and the whole mirror, archive and restore path run against a MinIO container (`hd-ev-minio-<random>` on a random loopback port, removed afterwards; image from `HEIMDALL_TEST_MINIO_IMAGE`, else `minio/minio`, else `bitnamilegacy/minio`), in path-style and virtual-host addressing, with multipart objects and a tampered object refused.
