package reportcard

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// CapabilityAll names the pooled score over every benchmark in a gate rule.
const CapabilityAll = "all"

// Gate rule names (schemas.GateCheck.Rule).
const (
	RuleMinScore      = "min_score"
	RuleMaxRegression = "max_regression"
)

// GateInput is what a gate is judged on: the candidate arm of one report,
// and optionally the baseline arm of the same or another report.
type GateInput struct {
	Gate             schemas.EvaluationGate
	Agent            string
	Candidate        string // arm label in Report
	CandidateVersion int
	Report           *Report
	// CompareReport is the candidate over the tasks it shares with the
	// baseline when the benchmark changed since (CompareDatasets); the
	// regression checks read it. nil uses Report.
	CompareReport *Report
	// Dataset is what CompareDatasets found; it is recorded on the result.
	Dataset        *schemas.DatasetComparison
	Baseline       *schemas.GateBaseline // nil: no baseline found
	BaselineArm    string                // arm label in BaselineReport
	BaselineReport *Report
	Now            time.Time
}

// scoreOf returns an arm's clean score on a capability ("all" is the pooled
// score over every benchmark) and whether it has runs there.
func scoreOf(r *Report, label, capability string) (float64, bool) {
	if r == nil {
		return 0, false
	}
	if capability == CapabilityAll {
		t, ok := r.Totals[label]
		return t.Score, ok && t.Runs > 0
	}
	for _, c := range r.Capabilities {
		if c.Capability == capability {
			m, ok := c.Arms[label]
			return m.Score, ok && m.Runs > 0
		}
	}
	return 0, false
}

func pts(x float64) string { return fmt.Sprintf("%.0f%%", x*100) }

// JudgeGate applies a gate. Every min_score rule is checked against the
// candidate; with max_regression set, every capability both sides ran (and
// the pooled score) must not drop by more than it. A regression limit with
// no baseline gives no_baseline unless another check already failed.
func JudgeGate(in GateInput) *schemas.EvaluationGateResult {
	res := &schemas.EvaluationGateResult{
		Agent: in.Agent, Candidate: in.Candidate, CandidateVersion: in.CandidateVersion,
		Baseline: in.Baseline, Checks: []schemas.GateCheck{}, DecidedAt: in.Now.UTC(), Dataset: in.Dataset,
	}
	cand := in.Report
	if in.CompareReport != nil {
		cand = in.CompareReport
	}
	caps := make([]string, 0, len(in.Gate.MinScore))
	for c := range in.Gate.MinScore {
		caps = append(caps, c)
	}
	slices.Sort(caps)
	failed := 0
	for _, c := range caps {
		min := in.Gate.MinScore[c]
		got, ok := scoreOf(in.Report, in.Candidate, c)
		chk := schemas.GateCheck{Capability: c, Rule: RuleMinScore, Threshold: min, Observed: got, Pass: ok && got+1e-9 >= min}
		switch {
		case !ok:
			chk.Summary = fmt.Sprintf("no %s runs to score", c)
		case chk.Pass:
			chk.Summary = fmt.Sprintf("%s score %s, at least %s needed", c, pts(got), pts(min))
		default:
			chk.Summary = fmt.Sprintf("%s score %s is below the %s needed", c, pts(got), pts(min))
		}
		if !chk.Pass {
			failed++
		}
		res.Checks = append(res.Checks, chk)
	}
	noBaseline := false
	if in.Gate.MaxRegression > 0 {
		if in.Baseline == nil || in.BaselineReport == nil {
			noBaseline = true
		} else {
			var both []string
			for _, c := range cand.Capabilities {
				_, a := scoreOf(cand, in.Candidate, c.Capability)
				_, b := scoreOf(in.BaselineReport, in.BaselineArm, c.Capability)
				if a && b {
					both = append(both, c.Capability)
				}
			}
			slices.Sort(both)
			both = append(both, CapabilityAll)
			for _, c := range both {
				got, a := scoreOf(cand, in.Candidate, c)
				was, b := scoreOf(in.BaselineReport, in.BaselineArm, c)
				if !a || !b {
					continue
				}
				w := was
				drop := was - got
				chk := schemas.GateCheck{Capability: c, Rule: RuleMaxRegression, Threshold: in.Gate.MaxRegression, Observed: got, Baseline: &w,
					Pass: drop <= in.Gate.MaxRegression+1e-9}
				if chk.Pass {
					chk.Summary = fmt.Sprintf("%s %s, was %s (at most %s drop allowed)", c, pts(got), pts(was), pts(in.Gate.MaxRegression))
				} else {
					chk.Summary = fmt.Sprintf("%s fell from %s to %s, more than the %s drop allowed", c, pts(was), pts(got), pts(in.Gate.MaxRegression))
					failed++
				}
				res.Checks = append(res.Checks, chk)
			}
		}
	}
	switch {
	case failed > 0:
		res.State = schemas.GateFail
		var why []string
		for _, c := range res.Checks {
			if !c.Pass {
				why = append(why, c.Summary)
			}
		}
		res.Summary = "Gate failed: " + strings.Join(why, "; ")
	case noBaseline:
		res.State = schemas.GateNoBaseline
		res.Summary = "No baseline run to compare with, so the regression limit could not be checked"
	default:
		res.State = schemas.GatePass
		res.Summary = fmt.Sprintf("Gate passed: %d of %d checks met", len(res.Checks), len(res.Checks))
		if len(res.Checks) == 0 {
			res.Summary = "Gate passed: no rule set"
		}
	}
	if in.Dataset != nil && in.Dataset.Changed && in.Gate.MaxRegression > 0 && res.State != schemas.GateNoBaseline {
		res.Summary += ". " + in.Dataset.Summary // the regression checks read the shared tasks only
	}
	return res
}

// Side is one arm's numbers for a release summary.
func Side(r *Report, label string) schemas.ReleaseSide {
	s := schemas.ReleaseSide{Label: label}
	if r == nil {
		return s
	}
	s.EvaluationID = r.Evaluation
	for _, a := range r.Arms {
		if a.Label == label {
			id, v := AgentRef(a.Agent)
			s.Agent, s.Model, s.Version = id, a.Model, a.Version
			if s.Version == 0 {
				s.Version = v
			}
		}
	}
	if t, ok := r.Totals[label]; ok {
		s.Score, s.CILow, s.CIHigh, s.Runs = t.Score, t.CILow, t.CIHigh, t.Runs
		s.CostPerSolved, s.FalseCompletion, s.FaultSurvival = t.CostPerSolved, t.FalseCompletion, t.FaultSurvival
	}
	return s
}

// CapabilityScores pairs each capability's candidate score with the
// baseline's, when the baseline ran it.
func CapabilityScores(r *Report, cand string, base *Report, baseLabel string) []schemas.ReleaseCapabilityScore {
	var out []schemas.ReleaseCapabilityScore
	if r == nil {
		return out
	}
	for _, c := range r.Capabilities {
		m, ok := c.Arms[cand]
		if !ok || m.Runs == 0 {
			continue
		}
		row := schemas.ReleaseCapabilityScore{Capability: c.Capability, Candidate: schemas.ScoreCI{Score: m.Score, CILow: m.CILow, CIHigh: m.CIHigh, Runs: m.Runs}}
		if base != nil {
			for _, bc := range base.Capabilities {
				if bc.Capability != c.Capability {
					continue
				}
				if bm, ok := bc.Arms[baseLabel]; ok && bm.Runs > 0 {
					row.Baseline = &schemas.ScoreCI{Score: bm.Score, CILow: bm.CILow, CIHigh: bm.CIHigh, Runs: bm.Runs}
				}
			}
		}
		out = append(out, row)
	}
	return out
}
