# Evidence integrity

This page says what the bench checks about a run folder before a verdict, a leaderboard or a release rests on it, what a seal proves and what it does not, and the two rules that keep a verdict from quietly changing meaning: every assigned slot counts, and every metric has a direction. The run folder layout is DESIGN.md 16.1.

## What a seal is

When a run ends, the store writes `seal.json` into the run folder: the sha256 of every other file in the folder, the manifest hash, the time, and the seal hash of the run sealed just before it in the same experiment. The run's seal hash is the sha256 of `seal.json` itself. The store then appends `<run_id> <seal hash>` to the experiment's `seals.log` (fsync, under a lock, so the chain is linear even with many runs sealing at once) and makes the folder read-only (files 0444, folders 0555).

Two checks follow from that:

- **Run check** (`store.Verify`, and the same check inside `SealedRuns` and `Index.VerifyRun`): `seal.json` names the run; every file listed is present with the same hash; no file was added (a symlink or any other non-regular entry counts as added); and the seal hash is the one `seals.log` recorded for the run. The error names the first file that differs, in path order.
- **Chain check** (`store.VerifyChain`): every `seals.log` line is well formed and names a run once; each line's hash is the sha256 of that run's `seal.json`; each seal's `prev_seal` is the hash on the line before; and no sealed run folder is missing from the log.

## What is verified when

| Reader | Run check | Chain check | On a mismatch |
|---|---|---|---|
| Analysis: `check`, the analysis at the end of `run`, the API's verdict and diagnostics routes (they analyse on request) | Every counted run, inside `SealedRuns` | Once per analysis | Verdict INVALID, action fix harness. The reason names the run (id, task, arm, condition, trial) and the file, or the `seals.log` line |
| Leaderboard (CLI and `GET /leaderboard`) | Every counted, sealed run on the board | Not run | The run is left off the board with reason `seal mismatch`. An experiment whose newest analysis is INVALID is left off whole, as before |
| Run detail (`GET /runs/{id}`) | The run, when it is sealed | Not run | `seal_error` carries the reason beside the files, which are still served so the change can be inspected. A check that could not run is reported there too |
| `regate` | Every counted run | Not run | Refused before anything is written |
| `release` | Every counted run of the analysis | Once | Refused, whatever the override says |

A run that does not verify is still returned by `SealedRuns`, with its slot and `run.json` only: none of its files are parsed, its metrics are empty and the reason is in `Problems`. Nothing read from a changed folder can be trusted, and a changed file may not even parse, so failing closed this way keeps a tampered folder from crashing the analysis instead of invalidating it.

Regrades and analyses are child artifacts outside every seal (DESIGN.md 16.1). They are not in `seal.json` and adding one never breaks a seal. A regrade's `grade.json` carries the parent run's seal hash and a regate carries a content hash; `SealedRuns` checks both when a regrade is the grade source. Human reviews (`docs/human-review.md`) are child artifacts the same way: `reviews/<review_id>/review.json` carries the run's seal hash and its own content hash, and `reviews.log` chains the records; a review that does not verify is ignored and reported, and reviews apply only under grade source `reviewed`.

### Cost and the verification cache

Verifying a run hashes every file in it, and a run's gateway log can be tens of megabytes. Measured on the six recorded experiments (318 runs, 745 MB, warm page cache): the first full pass of `SealedRuns` plus the chain check over all six takes about 2.3 s; every later pass takes about 0.26 s, of which the seal work is a few tens of milliseconds and the rest is the JSON decoding the analysis did before seals were checked. `Index.VerifyRun` over all 318 runs takes about 55 ms once cached.

The store keeps the result of each run's file comparison in memory, keyed by the run folder and the seal hash, together with a stamp of every file in the folder: size, modification time, inode change time (Linux) and mode. A later check stats the files and reuses the result only when every stamp and the seal hash are unchanged; anything else hashes the folder again. The stamps are taken before and after hashing and the result is cached only when they agree, so a file written during the check is never cached as clean. I/O errors are never cached. Writing a file moves its change time, which user space cannot set back, so a reused result describes the same bytes unless the system clock itself was turned back; on systems where the bench does not read the change time, the cache relies on size, modification time and mode. The `seals.log` lookup is cheap and is never cached. A one-shot CLI command starts with an empty cache; the API server keeps it for its lifetime, so only the first verdict request after a start hashes the experiment.

## What a seal proves and what it does not

A seal proves that the files in a run folder are the bytes they were when the run was sealed, and the chain proves that no sealed run was removed from, reordered in or inserted into the experiment's sequence afterwards, as long as `seals.log` itself was not rewritten consistently.

It does not prove:

- **Anything against someone with write access to the run folder.** The hashes are local. Whoever can rewrite a file can rewrite `seal.json` and `seals.log` to match, and the checks pass. There is no external anchor: no signature, no timestamping service, no copy of the chain held elsewhere. Publishing the `seals.log` tail (or a release record, whose own chain covers the seal hashes it used) somewhere the operator cannot edit is the way to get one.
- **That the evidence was right when it was sealed.** A seal freezes what the harness wrote. A wrong grade, a missed gateway row or a bug in a metric is sealed as faithfully as a correct one. The gateway log, the hidden-test regrade and the instrument checks are what speak to correctness.
- **Anything about files outside run folders.** The manifest is protected by its own hash, which every run records; regrades and analyses by their parent seal and content hashes; the suite by its lock hash. A lock with a fingerprints section also binds every evaluator setting and file of the suite, and the manifest records `fingerprint_hash` and the image ids it planned with (`suite_images`), so plan and run refuse a suite, task, probe or image that drifted from it (`docs/lock-fingerprints.md`). A legacy lock binds only its selected files. Adding fingerprints to a lock keeps its legacy fields byte for byte, so an experiment planned under the legacy lock still resumes and regates, and analysis of sealed runs never reads the live lock.

## Slot coverage

The manifest assigns every slot before the first run, and DESIGN.md 9.3 says no slot is dropped. Each counted slot must end with exactly one counted sealed run.

- **A slot with no sealed run** (a crash that was never resumed, a deleted folder) is added back to the analysis as an unknown, from the manifest. It counts in its task's cell, so it widens the missingness bounds and lowers graded coverage, and the coverage gate (0.90 per arm, 5 pp gap) decides whether the experiment is still valid, exactly as for a run whose grade is unknown. Trials per arm stay equal, so a missing run is never misread as unequal trials. The `slot_coverage` instrument check warns and names the missing slots. A task with no run at all stays in the comparison as unknowns instead of vanishing from it. The release policy is handed the same completed list, so a policy rule sees the same slots as the verdict.
- **A slot with more than one counted run** makes the verdict INVALID with the reason `slot <id> has N counted runs (...)`. The store's rule is that the counted run is the lowest-attempt sealed run whose outcome is not `aborted_infra`; `aborted_infra` attempts are retries and are never counted, so a slot with retries has one counted run. Two counted runs mean that rule was broken, and picking one silently would make the verdict depend on which. The leaderboard leaves such a slot out with reason `slot has more than one counted run`; `regate` and `release` refuse.

A deleted run folder is caught twice: its slot comes back as an unknown, and its line in `seals.log` no longer has a folder, so the chain check makes the verdict INVALID. A run that never sealed has no line in `seals.log` and is only an unknown.

## Metric direction

Every metric descriptor says whether a rise is better (`higher_is_better`). The verdict reads B minus A through that direction: on a lower-is-better primary endpoint (`cost_usd`, `tokens`, `steps`, `wall_time_s`, `false_completion`, ...) a significant fall is IMPROVED and a significant rise is REGRESSED, and a `no_regression` guard fails on a significant rise. The lower bound of an effect is always the one with the unknowns set against B, which on a lower-is-better metric means B's unknowns high and A's low. The verdict package cannot import the metrics package, so it keeps the list of lower-is-better and rate metrics itself (`verdict/direction.go`), and a test in `cmd/bifrost-bench` checks it against every registered descriptor. The release policy reads the same list, so a policy rule and a guard never disagree on which way is better. A metric neither list knows, such as a derived reward dimension, is read as higher-is-better.

## Unknown values and their bounds

An unknown value enters the missingness bounds (DESIGN.md 9.3 item 5) at both extremes. When the value carries its own bounds, those are used. When it does not, it is taken to lie in [0, 1] only on a rate metric, where that is true by definition. On any other metric an unknown with no bounds could be anything, so the effect has no missingness bounds at all: the verdict on that endpoint cannot be IMPROVED or REGRESSED, a `no_regression` guard on it is undetermined (which blocks IMPROVED), and the policy's rule checks report unknown. The point estimate on the observed runs is still shown.
