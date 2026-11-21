# Compliance evidence pack

A compliance pack maps the tests an experiment executed to the OWASP and NIST ids they give evidence for, states what the bench keeps for record-keeping, and links every row to the sealed runs behind it. It is a document for a security reviewer or a procurement questionnaire. It is not a certificate, a conformity assessment or legal advice, and it says so in itself.

## Commands

```
bifrost-bench compliance report -exp <id> [-exp <id>...] [-analysis <id>] [-o dir] [-format md|json|both]
bifrost-bench release export <release_id> [-o path] -compliance
GET /api/bench/experiments/{id}/compliance            # JSON attachment
GET /api/bench/experiments/{id}/compliance?format=md  # Markdown attachment
```

`compliance report` writes `compliance-<exp>.md` and `.json`. With several experiments the pack covers all of them and the file is named after all ids. `-analysis` picks the analysis of a single experiment; the default is the newest analysis on disk, and an experiment with none is analysed first (as the verdict route does). `release export -compliance` writes `compliance-<release_id>.md` and `.json` beside `-o` (or in the current directory), reports the analysis the release was recorded on and carries the release record: its hash, seals digest, approver and any override. It is built from the live store only while that store still holds the release's evidence: the counted runs' seals must match the release's seals digest and the runs, grades and reviews the analysis's inputs digest. After a later review, regrade or run it refuses, and the release's evidence ZIP stays the record of what was approved.

The verdict page links the Markdown and JSON downloads.

## What is in a pack

| Section | Content |
|---|---|
| Experiments | Suite, manifest hash, counted runs, how many seals verified when the pack was built, the `seals.log` chain status and its last line, the verdict and action of the analysis, the mode (live, recorded, synthetic) |
| Read first | Warnings: a broken chain, a run with evidence problems, no analysis, a smoke or synthetic experiment, a suite that could not be loaded |
| Tests executed | One row per condition (clean and every probe that ran) and per gate evaluated: runs, firings, and the OWASP LLM 2025, OWASP Agentic 2026 and NIST AI RMF ids, with how the row was mapped |
| Evidence per test | For each row, the runs behind it: run id, task, arm, trial, outcome, whether it fired, seal hash and run folder relative to the runs root. Fired runs are listed first; Markdown shows 12 per row, JSON shows all |
| NIST AI RMF, pack level | GOVERN, MEASURE and MANAGE subcategories the pack speaks to as a whole, with what it offers and what it does not |
| Record-keeping | What a run folder keeps, where, for how long, and how that relates to EU AI Act Articles 12, 26(5), 26(6) and 73 |
| Ids cited | Every id the pack names, its name, whether it was verified and the source |
| Not certified | What the pack does not show |

A condition row counts counted runs and, as fired, the runs where any veto gate fired. A gate row counts the runs it was evaluated on and the runs where it fired.

## Mapping

The mapping is a table in `compliance/catalog.go`, keyed by probe id and by gate name. A probe the table does not name is mapped by its axis, then by its PS2 family, and the row says so (`mapped_by: axis`); a gate it does not name is listed under unmapped. Nothing is dropped. A test checks that every built-in probe is mapped and that the gates which shipped before the pack keep a mapping; gates added later are logged until someone maps them.

Examples:

| Test | Ids |
|---|---|
| `prompt-injection`, `g12_injection_followed` | LLM01:2025, ASI01, MEASURE 2.7 |
| `tool-poison`, `poisoned-ticket`, `tool_injection_followed` | LLM01:2025, ASI01, ASI02, MEASURE 2.7 |
| `destructive-bait`, `g10_destroyed_user_work` | LLM06:2025, ASI02, MEASURE 2.6 |
| `secret-canary` | LLM02:2025, ASI03, MEASURE 2.7 |
| `describe-poison`, `schema-poison`, `mcp_poisoning_followed`; `rug-pull` | LLM01:2025, ASI01, ASI04, MEASURE 2.7; LLM03:2025, ASI04 |
| `poisoned-peer`, `peer_injection_followed`, `cross-agent-canary`, `relayed-consent` (team runs) | LLM01:2025 or LLM02:2025, ASI07, MEASURE 2.7 |
| `alert-noise`, `oversight_disabled` | ASI10, MEASURE 2.7 |
| `memory-trap`, `g13_memory_trap_followed` | ASI06, MEASURE 2.6 or 2.7 |
| `g1_protected_paths`, `g4_regrade`, `impossible_pass` | ASI10, MEASURE 2.5 (reward hacking) |
| `g8_false_completion` | LLM09:2025, ASI09, MEASURE 2.8 |
| `model-outage`, tool faults | MEASURE 2.6, MEASURE 2.5; no OWASP id covers availability |
| `clean` | MEASURE 2.3, MEASURE 2.5; not a security test |

A mapping says a test bears on an id. It does not say the risk is controlled.

## Sources and verification

Checked on 18 Nov 2026:

| Framework | Status | Source |
|---|---|---|
| OWASP Top 10 for LLM Applications 2025 (LLM01:2025 to LLM10:2025) | Verified: ids and names read from the list page | https://genai.owasp.org/llm-top-10/ |
| OWASP Top 10 for Agentic Applications 2026 (ASI01 to ASI10) | Unverified: the primary page (published 9 Dec 2025) offers the list as a download that was not read; names are from a secondary summary | https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/ and https://www.giskard.ai/knowledge/owasp-top-10-for-agentic-application-2026 |
| NIST AI RMF 1.0 subcategories (GOVERN 1.5, 4.3, 6.1; MEASURE 1.1, 2.1, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8; MANAGE 4.1, 4.3) | Verified: statements read from the AI RMF Playbook | https://airc.nist.gov/airmf-resources/playbook/ |
| EU AI Act Articles 12(1), 12(2), 26(5), 26(6) | Verified against an unofficial rendering; the Official Journal text governs | https://artificialintelligenceact.eu/article/12/ and /article/26/ |
| EU AI Act Article 73 | Unverified: taken from the safety research notes, not re-read | https://artificialintelligenceact.eu/article/73/ |
| Digital Omnibus deferral of Annex III obligations to 2 Dec 2027 | Unverified: a law firm summary | https://www.lewissilkin.com/en/insights/2026/07/27/the-digital-omnibus-on-ai-enters-into-force-today-102nedo |

Unverified ids are starred in the Markdown and carry `verified: false` in the JSON.

## Record-keeping

Article 12 asks high-risk systems to log events automatically; Article 26(6) asks deployers to keep the logs under their control for at least six months. The bench's run folder (DESIGN.md 16.1) keeps, per run: `run.json`, the agent stream, every gateway row for the run's virtual key, the fault proxy and tool proxy ledgers, normalized events and the step timeline, observer records, the workspace diff, receipts, gates, metrics, grade and cost, and `seal.json`; `seals.log` chains the runs of an experiment. Analyses, regrades and reviews are child records carrying the parent seal hash.

Retention: this copy of the bench has no retention setting. It never expires or deletes a run folder, so a six-month window is met only if the operator keeps the folders. There is no external anchor: seals are local hashes.

These are records of test runs. They are evidence for monitoring before deployment, not the deployed system's own lifetime logs.

## What a pack does not certify

- It is not a conformity assessment, an audit opinion or legal advice.
- OWASP lists are community guidance, not regulation.
- Coverage is only the probes and gates that ran on the listed tasks. A risk with no row was not tested.
- Gates see only what their witnesses see (gateway rows, tool proxy ledger, observers, workspace diff).
- Probes are static; no adaptive attacker searched for a bypass.
- Small samples cannot resolve small rates.
- Seals are local hashes with no external anchor.
- Recorded and synthetic experiments are not live measurements; the pack names them.
