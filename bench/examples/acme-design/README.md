# Acme design example: tasks graded on the files the agent writes

This folder is a complete Heimdall (bifrost-bench) project for a company
whose agent produces files rather than code changes or tool calls: a CAD
mesh, a spreadsheet summary, a brand swatch sheet. It is laid out like
[`examples/acme-support`](../acme-support/README.md) and follows the
artifact suite template (`bifrost-bench suite init -template artifact`); the
user guide is [`docs/scenarios.md`](../../docs/scenarios.md). No Go code is
needed:

- **An artifact suite.** `acme-design`: three tasks from a small design
  studio's week. Each gives the agent a brief and input files in the
  workspace and names the files it must write there.
- **Graded on content.** After the agent exits a trusted helper copies each
  file the task declares (`output.artifacts` in `task.yaml`) out of the
  workspace into the run folder, where the seal covers it. The task's hidden
  verifier, Python standard library only, reads it read-only at
  `/grader/artifacts/<id>/<base name>` and parses it: the mesh's volume and
  edges, the CSV's numbers, the SVG's elements and colours. It never reads
  the workspace, so a reward file or a fake output the agent leaves there
  counts for nothing.
- **Missing output fails early.** Every artifact here is `required: true`:
  when the file is missing the run fails with "artifact output: required
  artifact missing" and the verifier does not run.
- **Dimensions, not a blend.** Each verifier reports one dimension per
  property it checks (for example `geometry` and `watertight`); the reward
  is 1 only when every dimension is 1.
- **Controls per task.** A reference script that must pass, an empty
  control, an exploit and two plausible wrong outputs (mutant scripts) that
  must fail. Every control is a script run in the agent's place, and the
  files it leaves are exported and graded like an agent's.
- **One experiment.** A free smoke check with the scripted controls.

## What is fictional

Acme Design, its clients (Birchwood Bakery, Harbor Lane Books, Marlow Cycle
Works, Quillfeather Studio, Tidewater Florist, Larkspur Ferries), the
bracket drawing, the invoices and the brand palette are invented for this
example; nothing is taken from a real company or an existing benchmark.
Everything here was written from scratch and is MIT licensed
(`provenance.license` in every `task.yaml`, `created_by: hand-written
example`).

## Folder map

```
acme-design/
  README.md                           this file
  .gitignore                          runs/ and checks/ stay local
  deploy/
    bench.yaml                        paths, gateway, networks, helper image bench/coding-core-base:1
    pricing-snapshot.json             model prices for cost metrics (copy of the bench's)
  policies/default.yaml               release policy (copy of the bench's)
  agents/
    oracle.yaml, noop.yaml            scripted controls for the free smoke run
  benchmarks/acme-design/
    suite.yaml                        track artifact, output artifact, verifier_command with a reward
    probes/model-outage.yaml          gateway fault (copy of the bench's)
    tasks/<task>/
      task.yaml                       output.artifacts, hidden verifier, controls, provenance
      instruction.md                  what the agent is asked, naming the files to write
      repo/                           the workspace: the brief and the input files
      verifier/tests/verify.py        hidden: parses the exported artifacts, writes the reward
      verifier/requirements.md        what each dimension holds, and the instruction line it comes from
      solution/reference.sh           reference: writes the files a correct agent writes
      controls/empty.sh               writes nothing (must fail)
      controls/exploit.sh             copies the input to the output names and plants reward files (must fail)
      controls/mutant-*.sh            plausible wrong outputs (must fail)
  experiments/
    design-smoke.yaml                 free: oracle vs noop on all tasks
```

The suite's track is `artifact`: the agent produces files graded on their
content, so these tasks get a leaderboard of their own.

## The tasks

| Task | Artifacts (id: path) | What a correct agent writes | Dimensions | Mutants (must fail) |
|---|---|---|---|---|
| `bracket-mesh` | `mesh: bracket.obj` | An ASCII Wavefront OBJ of the bracket blank: a solid 40 x 25 x 10 mm block filling [0,40] x [0,25] x [0,10], closed, faces wound counter-clockwise seen from outside | `geometry`: bounding box within 0.01 mm and enclosed volume 10000 mm^3 within 0.5 percent (signed volume over the triangulated faces); `watertight`: every edge shared by exactly two faces, every directed edge used once, signed volume positive | a block 12 mm thick; a block with no top face |
| `invoice-summary` | `summary_csv: summary.csv`, `summary_json: summary.json` | Per-client totals of `data/invoices.csv` without void invoices (`client,invoices,total_eur,open_eur`, sorted, two decimals), and the overall totals and client count as JSON | `csv`: header, rows, counts and amounts within 0.005; `json`: `total_eur` 10937.60, `open_eur` 5940.85, `clients` 4 | void invoices counted (an extra Tidewater Florist row); `open_eur` repeating the total |
| `palette-swatch` | `swatch: palette.svg` | An SVG with one labelled `<rect>` per colour of the brief, in order, with the exact hex fill and a `<title>` naming the colour | `svg`: well-formed, root `svg` in the SVG namespace; `colors`: five rects, fills and titles in the brief's order; `layout`: each at least 40 x 40, none overlapping, no `transform` | Signal Coral one digit off (#E85D4B); Sandstone and Moss swapped |

Each `exploit.sh` copies an input file to the output name (the drawing note
as `bracket.obj`, the invoices as `summary.csv` with `{}` as
`summary.json`, the brief as `palette.svg`), writes `reward.json` and
`grader/out/reward.json` in the workspace and tries `/grader/out/reward.json`.
The verifier reads only the exported artifacts and its own expected values.

## Commands

All from this folder, with `bifrost-bench` built from `bench/`
(`CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench`)
and on `PATH`. The suite's base image is the bench helper image
`bench/coding-core-base:1`. Lint, lock and admission need no gateway
tokens; the smoke run needs the bench's stack running (see
`examples/acme-node`) with that stack's `.env` loaded.

```sh
cd bench/examples/acme-design

# 1. Check, lock and admit the suite. Admission runs every control in Docker
#    in the agent's place, exports the files it left and grades them.
bifrost-bench suite lint -config deploy/bench.yaml acme-design
bifrost-bench task validate -config deploy/bench.yaml acme-design/bracket-mesh
bifrost-bench suite lock -config deploy/bench.yaml acme-design
bifrost-bench suite admit -config deploy/bench.yaml acme-design

# 2. Free smoke run: oracle must verify 3 of 3, noop 0 of 3.
bifrost-bench plan experiments/design-smoke.yaml
BENCH_SERVER_MODE=synthetic bifrost-bench run experiments/design-smoke.yaml

# 3. Add a task in the same shape (EDIT ME markers until you rewrite it).
bifrost-bench task init -config deploy/bench.yaml -suite acme-design -id logo-export
```

Run one verifier by hand against files you made, from the suite folder:

```sh
mkdir -p /tmp/art/mesh && cp bracket.obj /tmp/art/mesh/
GRADER_ARTIFACTS=/tmp/art GRADER_OUT=/tmp/out python3 tasks/bracket-mesh/verifier/tests/verify.py
```

## Status

What was run, and what was not, on 15 Nov 2026:

- **Run on the host:** every control of every task, outside Docker: the
  task's `repo/` copied to a temporary workspace, the control run there with
  `sh`, each declared artifact copied to `<id>/<base name>` and the task's
  `verify.py` run on it. Rewards (MISSING: a required artifact was not
  written, so the bench fails the run before the verifier):

  | Task | reference | empty | mutant 1 | mutant 2 | exploit | noop |
  |---|---|---|---|---|---|---|
  | `bracket-mesh` | 1 | MISSING | 0 (`geometry` 0) | 0 (both 0) | 0 | MISSING |
  | `invoice-summary` | 1 | MISSING | 0 (both 0) | 0 (both 0) | 0 | MISSING |
  | `palette-swatch` | 1 | MISSING | 0 (`colors` 0) | 0 (`colors` 0) | 0 | MISSING |

- **Run in the base image:** each reference script and verifier inside
  `bench/coding-core-base:1` (Python 3.12, uid 1000, no network): reward 1
  on all three tasks.
- **Run:** `suite lint` (0 problems) and `task validate` on every task (no
  problems).
- **Not run here:** the smoke experiment. It needs the gateway's admin
  token and the fault proxy token from the stack's `deploy/.env`.

### Admission results

`suite lint` (0 problems), `suite lock` and `suite admit -config
deploy/bench.yaml acme-design` ran in Docker on 15 Nov 2026 with the suite
on `track: artifact`, and admitted all three tasks; the record is in
`benchmarks/acme-design/suite.lock`. For every task the reference passed 3
times in a row, and the empty control, both mutants, the exploit, noop and
refuse failed:

| Control | Got | Why (from the admission record) |
|---|---|---|
| `reference_1..3` | pass | `reward 1` |
| `empty_control`, `null_noop`, `null_refuse` | fail | `artifact output: required artifact missing: ...`; the verifier is not run |
| `mutant_1`, `mutant_2` | fail | `reward 0 below pass threshold 1` |
| `exploit` | fail | `reward 0 below pass threshold 1`: the planted reward files are never read |

`instruction_edited`, `secret_scan` and `verifier_no_network` passed for
every task.

## Honest limits

- The mesh check is a box check: bounding box, enclosed volume and a closed,
  consistently wound surface. It does not detect self-intersections, and a
  different solid with the same bounding box and volume would need more
  checks; for a block that fills its bounding box the two coincide.
- The invoice and palette verifiers hold their expected values, copied once
  from the task's input. Changing `data/invoices.csv` or `brief.md` without
  changing `verify.py` breaks the reference, which admission then reports.
- `palette-swatch` checks the swatches' colours, names, order and layout,
  not how the sheet looks; text labels and outlines are allowed and not
  graded.
- Three tasks decide little: with this many tasks most comparisons read
  inconclusive, and the report says how many tasks a decision needs.
