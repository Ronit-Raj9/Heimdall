# heimdall-improver

The improver task of improvement runs, `heimdall-improver@1.1.0:reflect-edit`.
It is shipped with Heimdall and copied into a project the first time an
improvement run needs it. It is tagged `internal` and is not meant to be run
as a benchmark.

## What the task asks

The improver agent (by default `agents/heimdall-reflector.yaml`) reads the
evidence of an agent's practice runs, mounted read-only under
`/heimdall/improve/`:

| Path | What it holds |
|---|---|
| `bundle.json` | the agent's editable files, the numbered rules and the limits |
| `evidence.json` | the practice runs, their failure clusters and prune hints |
| `history.json` | earlier candidates: their edits, practice result and round sentence |
| `limits.json` | the size caps of an edit set |
| `runs/<task>/<trial>/<arm>/` | `digest.md`, `transcript.jsonl` and `diff.patch` of each practice run |

It writes `/workspace/edits.json`: a rationale and at most 8 edits (add,
edit or delete a numbered rule; add, edit or retire a skill; on a restart,
rewrite a whole file). Each edit cites the practice runs it comes from.

## How it is graded

The verifier checks the form of `edits.json` only: valid JSON of the edits
schema, at most 8 edits, known operations, every file or path absolute (or
`@instructions`) and outside system folders, and text sizes under the caps.
Whether the cited runs exist, and every other rule, is checked when the
edits are applied to the agent's files.

## Controls

- `solution/reference.sh` writes one valid rule that cites the first practice run (pass).
- `controls/empty.sh` writes nothing (fail: the required file is missing).
- `controls/exploit.sh` writes an edit to `/etc/passwd` (fail).
- `controls/mutant-too-many.sh` writes nine edits, one over the cap (fail).
