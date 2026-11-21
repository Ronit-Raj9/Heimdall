# Improvement runs

An improvement run lets your agent improve its own prompt, notes and skills over N rounds (default 3), and measures the result so the gain can be trusted. Code: `bench/improve/`, CLI `bifrost-bench improve`, API `/api/bench/improvements`, page `/improvements/$id`.

## Spec

```yaml
# improvements/coder-notes.yaml
kind: improvement
name: coder-notes
agent: agents/coder.yaml          # version 0; a plain file is snapshotted first
suite: coding-core@1.0.0          # the task pool
rounds: 3
seed: 41
editable:                         # the bundle: container paths in the agent's files map
  files: [/home/agent/AGENTS.md]
  dirs: [/home/agent/.claude/skills/]
splits: {dev: 0.4, audit: 0.3, sealed: 0.3, honeypots: [t09-impossible]}
improver:
  task: improver@1.0.0:rewrite-bundle   # output kind artifact, leaves bundle.json
  budget_usd: 2.00                       # per round, the run's gateway key cap
  wall_s: 1800
screen: {trials: 1}
commit: {alpha: 0.05, min_pairs: 4, max_pairs: 60, trials: 3, audit_looks: 3}
sealed: {trials: 2}
controls: [budget_matched_v0, null_improver]
```

`frozen` defaults to adapter, image, build, argv, env, model, budgets, gateway, capabilities, limits and the adapter blocks; the improver can never change them.

## One round

1. **Improve.** A normal sealed run of the incumbent on the improver task. Its agent config is the incumbent's plus `/heimdall/improve/bundle.json` (the current bundle) and `/heimdall/improve/evidence.json` (the incumbent's dev results: task, trial, outcome, success, veto gate names; never verifier content). The run's key is capped at `improver.budget_usd`. No verifier, audit or sealed task and no run store is mounted. The task's artifact `bundle.json` (`{"files": {"<path>": "<text>"}}`) is the new bundle.
2. **Freeze.** The bundle is applied to the incumbent's YAML (only editable paths) and stored as an agent version with `parent`, `author: agent`, `improvement`, `round`, `produced_by_run` and `bundle_hash` (`agentversions.Derive`). The improvement's `lineage.jsonl` gets a hash-chained entry; rejected candidates stay in the lineage.
3. **Integrity gates** on the bundle diff: `improver_answer_key` (gate or held-out task ids, 8-word overlaps with hidden tests, expected answers and required test names), `improver_canary` (the protected benchmark's canary GUID), `improver_frozen_field` (a path outside the bundle or a frozen field changed), `improver_eval_cue` (the bench's own cues: `BENCH_`, `toolproxy`, `/opt/bench`, canary wording, grader and verifier words), and after the screen `improver_monitor_parity` (the candidate stopped emitting an event channel its parent emits). Spend at 90% of the cap and an `egress` or `oversight_disabled` fire on the improver run are tripwires too. Any tripwire pauses the run at `awaiting_approval`; `approve` with a reason continues and the override is recorded and blocks a "supported" verdict.
4. **Screen** on dev: incumbent vs candidate, paired. A significant regression (sign-flip p < 0.05) rejects cheaply.
5. **Commit gate** on audit (plus honeypots): the paired per-(task, trial) differences, in task then trial order, feed the betting e-process in `stats/evalue.go` (the same one live scoring uses). The candidate is kept only when the e-value reaches `rounds / alpha` (alpha split over the rounds, so the chance of any false commit over the loop is at most alpha), no honeypot passed and vetoed runs did not rise by more than one. Every look uses one of `audit_looks`.
6. **Held-out scoring.** The incumbent is scored on the sealed split every round. Nothing in the loop reads it; only the report does (tested: moving only the sealed scores changes no decision).

## Controls and verdict

- `budget_matched_v0`: version 0 given the loop's own spend (improver + screen + audit) as extra trials on sealed; the report shows the band (mean plus or minus 1.96 SE over tasks) and best-of-k.
- `null_improver`: the same rounds with no evidence, every edit kept, the last version scored on sealed.

`supported` needs at least one commit, a positive sealed gain over version 0 with sign-flip p < alpha, a final sealed score above the band and the null improver, and no override. Otherwise `not_supported`, `inconclusive` or `no_change`. A single loop is noted; reruns of the whole loop are not built yet.

## Storage

`<runs root>/.improvements/<id>/improvement.json` (frozen spec and hash, partition and hash, rounds, controls, verdict), `protected.json` (0600, never served), `lineage.jsonl` (hash-chained). A failed experiment leaves status `failed`; `improve run <id>` resumes from the saved stage.

## API

`GET /improvements`, `POST /improvements` (spec as YAML or JSON, 202, starts in the background), `GET /improvements/{id}`, `GET /improvements/{id}/rounds/{k}`, `GET /improvements/{id}/lineage`, `POST /improvements/{id}/rounds/{k}/approve` (`{"reason": "..."}`), `POST /improvements/{id}/stop`. The report never serves audit or sealed task ids, the task pool, or the agent YAML. On a hosted server, create and approve are for org admins (they spend the organization's keys) and stop is open to any member. Every experiment of the run goes through the organization's launcher like any other run: it needs a working key of the organization for each provider (no fallback to server keys), counts against the daily caps when a server admin set them, and runs on the organization's default worker (the shared worker unless the organization chose its own).
