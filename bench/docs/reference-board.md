# Reference board

The reference board shows how known agents (Codex, Claude Code, mini-SWE-agent
and others) score on the built-in benchmarks, so a new user sees real numbers
before their first evaluation and has an anchor after it.

Every number on it comes from real runs the server operator ran, in an
operator-owned organization called **Heimdall reference** (slug
`heimdall-reference`). Nothing is typed in by hand and there is no sample
data: an empty board says that nothing is published yet.

## Where it shows

- `/reference`: a public, read-only page. No sign-in.
- Leaderboard, **Reference** tab.
- A built-in benchmark's page: "How known agents score here".
- The evaluate wizard, step 3: "codex scores about 42% here" under a benchmark
  that has an entry.
- API: `GET /api/bench/reference/board[?benchmark=<id>]`, public.

Each entry is one agent version on one model on one benchmark: score with its
95% range, cost per solved task, runs and tasks, the date the newest run
finished, a fingerprint (the last seal hash of the experiment's chain) and a
link to the evaluation's read-only report card.

## What publishing checks

`bifrost-bench reference publish <evaluation>` refuses unless:

- the evaluation is in the reference organization and is done;
- it is not a held-out evaluation (publishing would expose the held-out tasks)
  and not a rerun of failures;
- every benchmark in it is a built-in library benchmark;
- every sealed run of every experiment verifies against its seal, every
  experiment's seal chain verifies, and the runs in the chain match the runs
  verified;
- at least one benchmark has counted runs. Practice runs (the scripted model)
  never count.

The scores are the report card's, built from those runs at publish time. A
share link to the report card that never expires is made (or the existing one
reused); unpublishing turns it off.

## Operator steps

Run every command inside the server container, where the database, the
storage root and `HEIMDALL_LIBRARY_DIR` are set:

```sh
C="docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env"
```

### A. Run in the reference organization (simplest)

1. Create the organization with yourself as owner:
   `$C exec heimdall bifrost-bench org create -slug heimdall-reference -name "Heimdall reference" -owner you@example.com`
2. Sign in, switch to Heimdall reference, add a provider key (Settings, Keys).
3. Evaluate a built-in agent (Codex, Claude Code, ...) on the library
   benchmarks: score mode, the benchmark's ready tasks, 1 to 3 trials.
4. When it is done, read its report card in the app.
5. `$C exec heimdall bifrost-bench reference publish ev_<id>`

### B. Run elsewhere and import

1. On a fleet machine: `bifrost-bench run` (or `eval run` locally) on the
   library benchmarks. Each experiment folder is sealed as it runs.
2. Copy the runs folder to the server and into the container:
   `$C cp ./runs heimdall:/tmp/ref-runs` (add `./.heimdall/evaluations` to
   `/tmp/ref-evals` when the runs came from `eval run`).
3. Import: `$C exec heimdall bifrost-bench reference import /tmp/ref-runs [-evaluations /tmp/ref-evals] [-owner you@example.com]`.
   Every seal and chain is verified on the copy; an experiment that fails is
   refused and nothing of it is kept. Each experiment no evaluation file
   covers becomes evaluation `ev_ref_<experiment id>`.
4. Check the report cards in the app (the reference organization), then
   `$C exec heimdall bifrost-bench reference publish ev_<id>` for each.

### Manage

- `reference list`: the board, then finished reference evaluations not
  published.
- `reference unpublish ev_<id>`: takes it off the board and turns its report
  card link off.
- Publishing the same evaluation again replaces its entries.
- Refresh monthly: run again, publish the new evaluation, unpublish the old.

The board is one file, `<storage root>/.reference/board.json`; it is
covered by the storage root backup.
