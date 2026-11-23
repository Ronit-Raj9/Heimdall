package release

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// childEvidence is one child experiment of a release of an evaluation.
type childEvidence struct {
	ref schemas.ReleaseChildRef
	m   *schemas.Manifest
	ev  *evidence
}

// createEvaluation records one release that covers every finished child of
// an evaluation. Each child's newest analysis and sealed runs are checked as
// a release of one experiment is (seals, chain, manifest, inputs digest:
// never overridable). The release is blocked when a child's policy blocks,
// when a child's verdict says the released version got worse, or for the
// reasons the server found on the evaluation as a whole (its gate, its
// overall verdict); an override reason releases against those and is
// recorded. A child whose verdict is only inconclusive does not block: an
// evaluation is judged as a whole.
func (l *Log) createEvaluation(ctx context.Context, r schemas.ReleaseRequest, e *schemas.ReleaseEvaluation) (*schemas.ReleaseRecord, error) {
	if len(e.Children) == 0 {
		return nil, &RefusedError{Reason: "the evaluation has no finished benchmark to release"}
	}
	if strings.TrimSpace(e.AgentID) == "" {
		return nil, &RefusedError{Reason: "the evaluation names no agent"}
	}
	if e.CandidateArm == "" {
		return nil, &RefusedError{Reason: "no version of the evaluation was picked to release"}
	}
	var children []childEvidence
	var blocked []string
	for _, c := range e.Children {
		if !idPattern.MatchString(c.ExpID) {
			return nil, fmt.Errorf("create release: invalid experiment id %q", c.ExpID)
		}
		m, err := l.d.Store.LoadManifest(ctx, c.ExpID)
		if err != nil {
			return nil, fmt.Errorf("create release: %s: %w", c.Benchmark, err)
		}
		ev, err := l.loadEvidence(ctx, m, "")
		if err != nil {
			return nil, err
		}
		if err := l.verifyEvidence(ctx, m, ev); err != nil {
			return nil, err
		}
		if err := checkIntegrity(m, ev); err != nil {
			if re, ok := err.(*RefusedError); ok {
				re.Reason = c.Benchmark + ": " + re.Reason
			}
			return nil, err
		}
		if m.Experiment.Debug != nil || strings.HasPrefix(m.Experiment.Name, "admit-") || strings.HasPrefix(m.ExpID, "e_admit_") {
			return nil, &RefusedError{Reason: c.Benchmark + ": " + notReleasable(m, nil)} // fix wave 3: a debug run or readiness check is never released
		}
		if _, ok := m.Arms[e.CandidateArm]; !ok {
			return nil, &RefusedError{Reason: fmt.Sprintf("%s: experiment %s has no arm %s", c.Benchmark, m.ExpID, e.CandidateArm)}
		}
		for _, b := range policyBlocks(m, ev) {
			blocked = append(blocked, c.Benchmark+": "+b)
		}
		if candidateRegressed(ev.analysis, e.CandidateArm) {
			blocked = append(blocked, fmt.Sprintf("%s: the verdict says %s got worse", c.Benchmark, e.CandidateArm))
		}
		children = append(children, childEvidence{ref: c, m: m, ev: ev})
	}
	blocked = append(blocked, e.Blocked...)
	if err := checkOverride(blocked, r.Override, "no benchmark regressed, no policy blocks and the evaluation's gate passed"); err != nil {
		return nil, err
	}
	rec := newEvaluationRecord(children, e, r, l.d.Now().UTC(), len(blocked) == 0)
	if err := l.appendRelease(rec, func(rec *schemas.ReleaseRecord) ([]packFile, error) {
		return evaluationPackFiles(l.d.Store.Root(), rec, children)
	}); err != nil {
		return nil, err
	}
	l.d.Logger.Info("release recorded", "release_id", rec.ReleaseID, "evaluation_id", rec.EvaluationID, "agent", rec.AgentID,
		"version", rec.Version, "approver", rec.Approver, "override", rec.Override != "")
	return rec, nil
}

// candidateRegressed reports whether an analysis says arm got worse: a
// regressed verdict when arm is the treatment (B), an improved one when arm
// is the control (A).
func candidateRegressed(a *schemas.Analysis, arm string) bool {
	if a == nil {
		return false
	}
	armA, armB := "A", "B"
	if eff := a.Verdict.Effect; eff != nil {
		if eff.ArmA != "" {
			armA = eff.ArmA
		}
		if eff.ArmB != "" {
			armB = eff.ArmB
		}
	}
	switch arm {
	case armB:
		return a.Verdict.Decision == schemas.DecisionRegressed
	case armA:
		return a.Verdict.Decision == schemas.DecisionImproved
	}
	return false
}

// policyRank orders policy decisions, worst last.
func policyRank(d schemas.PolicyDecision) int {
	switch d {
	case schemas.PolicyBlock:
		return 3
	case schemas.PolicyWarn:
		return 2
	case "":
		return 0
	}
	return 1
}

// newEvaluationRecord fills the record of a release of an evaluation. The
// experiment fields name the first child; Children names them all.
func newEvaluationRecord(children []childEvidence, e *schemas.ReleaseEvaluation, r schemas.ReleaseRequest, now time.Time, clear bool) *schemas.ReleaseRecord {
	first := children[0]
	am := first.m.Arms[e.CandidateArm]
	var all []string
	rec := &schemas.ReleaseRecord{
		ExpID: first.m.ExpID, AnalysisID: first.ev.analysis.AnalysisID, Arm: e.CandidateArm,
		AgentConfigID: am.Config.ID, ConfigHash: am.ConfigHash,
		Decision: e.Decision, Action: schemas.ActionHold, ManifestHash: first.m.Hash,
		Approver: r.Approver, Note: r.Note, Override: r.Override, CreatedAt: now,
		AgentID: e.AgentID, Version: e.Version, ReplacesVersion: e.ReplacesVersion,
		EvaluationID: e.EvaluationID, Summary: e.Summary, Gate: e.Gate,
	}
	if rec.Decision == "" {
		rec.Decision = schemas.DecisionInconclusive
	}
	if clear {
		rec.Action = schemas.ActionShip
	}
	for _, c := range children {
		hashes := make([]string, 0, len(c.ev.runs))
		for _, run := range c.ev.runs {
			hashes = append(hashes, run.SealHash)
		}
		all = append(all, hashes...)
		ch := schemas.ReleaseChild{
			Benchmark: c.ref.Benchmark, ExpID: c.m.ExpID, AnalysisID: c.ev.analysis.AnalysisID,
			Decision: c.ev.analysis.Verdict.Decision, Action: c.ev.analysis.Verdict.Action,
			ManifestHash: c.m.Hash, SealsDigest: SealsDigest(hashes), Runs: len(c.ev.runs),
		}
		if p := c.ev.policy; p != nil {
			ch.PolicyName, ch.PolicyDecision = p.RuleSetName(), p.Decision
			if policyRank(p.Decision) > policyRank(rec.PolicyDecision) {
				rec.PolicyName, rec.PolicyHash, rec.PolicyDecision = p.RuleSetName(), p.PolicyHash, p.Decision
			}
		}
		rec.Children = append(rec.Children, ch)
	}
	rec.SealsDigest = SealsDigest(all)
	return rec
}

// evaluationPackFiles is the evidence pack of a release of an evaluation:
// the readme, the record, then each child's evidence under
// experiments/<exp_id>/ laid out as a release of one experiment is.
func evaluationPackFiles(runsRoot string, rec *schemas.ReleaseRecord, children []childEvidence) ([]packFile, error) {
	recJSON, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release.json: %w", err)
	}
	files := []packFile{
		{"README.txt", []byte(evaluationReadme(rec, children))},
		{"release.json", append(recJSON, '\n')},
	}
	used := map[string]bool{"README.txt": true, "release.json": true}
	for _, c := range children {
		if err := packExperiment(runsRoot, c.m.ExpID, c.ev, "experiments/"+c.m.ExpID+"/", &files, used); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// evaluationReadme says what the pack of a release of an evaluation holds.
func evaluationReadme(rec *schemas.ReleaseRecord, children []childEvidence) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	replaces := "no version was live before"
	if rec.ReplacesVersion > 0 {
		replaces = fmt.Sprintf("replacing v%d", rec.ReplacesVersion)
	}
	override := "none"
	if rec.Override != "" {
		override = rec.Override
	}
	line("Heimdall release evidence pack")
	line("")
	line("Release %s: agent %s v%d, %s.", rec.ReleaseID, rec.AgentID, rec.Version, replaces)
	line("Evaluation %s, %d benchmark(s). Gate: %s. Action %s.", rec.EvaluationID, len(children), orDash(rec.Gate), rec.Action)
	line("Approved by %s at %s. Override: %s.", rec.Approver, rec.CreatedAt.Format(time.RFC3339), override)
	line("")
	line("Benchmarks")
	for _, c := range children {
		line("  %-28s experiment %s, analysis %s, %d counted runs", c.ref.Benchmark, c.m.ExpID, c.ev.analysis.AnalysisID, len(c.ev.runs))
	}
	line("")
	line("Each experiments/<exp_id>/ folder is laid out as the pack of a release of")
	line("one experiment: manifest.json, analysis/verdict.json, analysis/report.md,")
	line("analysis/policy.json when a policy was evaluated, seals.log, chain/ and")
	line("runs/<run_id>/ with every sealed run file. release.json lists every child")
	line("with its seals_digest; the record's seals_digest covers every counted run")
	line("of every child (sort the seal hashes, one per line, sha256).")
	line("")
	line("The release log is hash-chained, not signed: the chain shows that a record")
	line("has not changed since it was written, not who wrote it.")
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
