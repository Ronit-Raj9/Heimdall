package compliance

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// packLevelIDs are the ids the pack speaks to as a whole, beside the per-test
// mappings: the NIST functions and the EU AI Act articles on logs.
var packLevelIDs = []string{
	"GOVERN 1.5", "GOVERN 4.3", "GOVERN 6.1", "MEASURE 1.1", "MEASURE 2.1", "MEASURE 2.4", "MANAGE 4.1", "MANAGE 4.3",
	"Art. 12(1)", "Art. 12(2)", "Art. 26(5)", "Art. 26(6)", "Art. 73",
}

// retainedFiles are the files a sealed run folder keeps (DESIGN.md 16.1 and
// the steps and scenario additions). A run keeps those its task shape and
// observers produced; the seal lists exactly which.
var retainedFiles = []schemas.ComplianceArtifact{
	{File: "manifest.json", Holds: "the frozen, hashed experiment: arms, agent configs, suite lock hash, seed, primary endpoint", Sealed: false},
	{File: "seals.log", Holds: "one line per sealed run, chained by the previous seal hash", Sealed: false},
	{File: "slot.json", Holds: "the slot assignment: task, arm, condition, trial", Sealed: false},
	{File: "run.json", Holds: "outcome, timings, agent identity, gateway key id", Sealed: true},
	{File: "agent.stream.jsonl", Holds: "the agent's raw output stream", Sealed: true},
	{File: "gateway.requests.jsonl / gateway.raw.jsonl", Holds: "every model request Bifrost logged for the run's virtual key, secret-scanned", Sealed: true},
	{File: "proxy.ledger.jsonl", Holds: "fault proxy decisions: which requests were faulted", Sealed: true},
	{File: "tools.jsonl", Holds: "the tool proxy ledger: every MCP, HTTP and external API call made", Sealed: true},
	{File: "events.jsonl / steps.jsonl", Holds: "normalized events and the step timeline, each row naming its witness", Sealed: true},
	{File: "processes and file events (observers)", Holds: "process observer and workspace observer records", Sealed: true},
	{File: "workspace.diff", Holds: "the exported change the agent made", Sealed: true},
	{File: "receipts.json", Holds: "per-probe receipts: assigned, fired, visible", Sealed: true},
	{File: "gates.json / metrics.json / grade.json / cost.json", Holds: "gate results, metric values, the grade and cost rows", Sealed: true},
	{File: "seal.json", Holds: "sha256 of every other file, the manifest hash and the previous seal hash", Sealed: true},
	{File: "analysis/<id>/, regrades/<id>/, reviews/<id>/", Holds: "derived verdicts, regrades and human reviews as child records carrying the parent seal hash", Sealed: false},
}

// recordKeeping states what the bench retains, where and for how long,
// beside the EU AI Act articles on logs.
func recordKeeping() schemas.ComplianceRecordKeeping {
	return schemas.ComplianceRecordKeeping{
		Retained:       retainedFiles,
		Location:       "runs/<exp_id>/ under the configured runs root (deploy/bench.yaml runs_root) on the host that ran the bench; release evidence packs under the releases root",
		Retention:      "Until the operator deletes them. This copy of the bench has no retention setting and never expires or deletes a run folder; keeping logs for at least six months (Art. 26(6)) is the deployer's duty and is met only if the folders are kept.",
		ExternalAnchor: false,
		Articles: []schemas.ComplianceMapping{
			withNote("Art. 12(1)", "The bench records each evaluated run automatically; this is test-time evidence, not the deployed system's own lifetime logging."),
			withNote("Art. 12(2)", "Every run keeps the model requests, tool calls, process and file events and the final change, each tied to its witness, enough to reconstruct the action chain of a test run."),
			withNote("Art. 26(5)", "Probe and gate results are evidence for monitoring before deployment; the bench does not monitor production traffic."),
			withNote("Art. 26(6)", "Run folders are kept indefinitely unless deleted, so a six-month window is met when the operator keeps them."),
			withNote("Art. 73", "A frozen manifest and sealed runs keep the evaluated system state replayable for an incident investigation."),
		},
		Notes: []string{
			"Seals are local sha256 hashes: they prove a run folder is unchanged since sealing against anyone without write access to it, not against its operator. There is no external anchor (signature or transparency log).",
			"Annex III high-risk obligations, including Article 26 for those systems, were deferred to 2 December 2027 by the Digital Omnibus on AI (in force 27 July 2026), per " + srcOmnibus + " (secondary source, not verified against the Official Journal).",
			"Article texts were read from artificialintelligenceact.eu, an unofficial rendering of Regulation (EU) 2024/1689; the Official Journal text governs.",
		},
	}
}

// withNote returns a catalog mapping carrying a note.
func withNote(id, note string) schemas.ComplianceMapping {
	m := mapping(id)
	m.Note = note
	return m
}

// nistFunctions states, per pack-level NIST AI RMF subcategory, what the pack
// offers and what it does not.
func nistFunctions(p *schemas.CompliancePack) []schemas.ComplianceFunction {
	rows := []struct{ id, evidence, gap string }{
		{"GOVERN 4.3", "A repeatable test practice: frozen manifests, admitted tasks, sealed runs and a release log.", "Organizational practices and information sharing are outside the bench."},
		{"GOVERN 6.1", "Third-party skills and MCP servers can be scanned before admission (bifrost-bench skill scan, mcp scan).", "The pack does not record whether they were; policies and procedures are the organization's."},
		{"GOVERN 1.5", "Schedules re-run suites on a timetable; trends compare runs over time.", "Review frequency and roles are not set by the bench."},
		{"MEASURE 1.1", fmt.Sprintf("%d tests mapped; %d unmapped tests listed by id; the not-certified list names what was not measured.", len(p.Tests)-len(p.Unmapped), len(p.Unmapped)), "Risk selection from a MAP function is not part of the bench."},
		{"MEASURE 2.1", "The manifest hash, suite lock hash and every part version are frozen before the first run and listed per experiment.", "Test set documentation beyond the suite files is the author's."},
		{"MEASURE 2.4", "None: the bench evaluates before deployment.", "Production monitoring is not covered."},
		{"MANAGE 4.1", "None: no post-deployment monitoring loop.", "Not covered."},
		{"MANAGE 4.3", "A failing production trace can become a frozen task (tasks from traces) and re-run on every release.", "Incident communication and response are the organization's."},
	}
	out := make([]schemas.ComplianceFunction, 0, len(rows))
	for _, r := range rows {
		e := catalog[r.id]
		out = append(out, schemas.ComplianceFunction{ID: e.id, Name: e.name, Verified: e.verified, Source: e.source, Evidence: r.evidence, Gap: r.gap})
	}
	return out
}

// notCertified is the honest list of what the pack is not, with the
// experiment-specific limits first.
func notCertified(p *schemas.CompliancePack) []string {
	var out []string
	for _, e := range p.Experiments {
		if e.Mode == schemas.ModeSynthetic || e.Mode == schemas.ModeRecorded {
			out = append(out, fmt.Sprintf("%s ran in %s mode: its runs are not a live measurement.", e.ExpID, e.Mode))
		}
	}
	return append(out,
		"This pack is not a conformity assessment, an audit opinion or legal advice. It maps test evidence to framework ids; a mapping says a test bears on an id, not that the risk is controlled.",
		"OWASP lists are community guidance, not regulation. OWASP Agentic 2026 ids and names are marked unverified: they come from a secondary summary because the primary list is published as a download that was not read.",
		"Coverage is only the probes and gates that ran on the listed tasks. A risk with no test row was not tested. Unmapped tests are listed by id.",
		"Gates see only what their witnesses see (gateway rows, tool proxy ledger, observers, workspace diff). Behaviour that crosses none of those boundaries is invisible to them.",
		"Static probes only: no adaptive attacker searched for a bypass, so a passing row is a lower bound on attack success, not a guarantee.",
		"Small samples: a handful of tasks and trials per condition cannot resolve small rates; read the counts, not the percentages.",
		"Seals are local hashes with no external anchor; they do not prove anything against whoever controls the run folders.",
		"Retention is whatever the operator keeps: the bench sets no retention period and deletes nothing.",
	)
}
