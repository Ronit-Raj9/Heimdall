package reportcard

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdictab"
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

// JudgeGate applies a gate (G4-07).
//
// Every min_score rule is checked against the candidate's score; a rule
// whose capability holds a judge-graded benchmark with a judge that is not
// calibrated is inconclusive instead, unless the gate allows uncalibrated
// judges.
//
// With max_regression set, every capability both sides ran (and the pooled
// score) is compared on paired tasks: only benchmarks the baseline ran at
// the same version, and on those only the tasks both sides ran. The rule
// fails only when the paired drop is significant (the 95% paired bootstrap
// interval of the change lies below zero) and larger than the limit; a drop
// over the limit the interval cannot tell from noise, or fewer than two
// paired tasks, is inconclusive; anything else passes. Each check carries
// the smallest change its paired tasks can detect.
//
// A regression limit with no baseline gives no_baseline unless another
// check already failed or was inconclusive.
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
	for _, c := range caps {
		min := in.Gate.MinScore[c]
		got, ok := scoreOf(in.Report, in.Candidate, c)
		chk := schemas.GateCheck{Capability: c, Rule: RuleMinScore, Threshold: min, Observed: got, Pass: ok && got+1e-9 >= min}
		untrusted := untrustedJudges(in.Report, c)
		switch {
		case !ok:
			chk.Summary = fmt.Sprintf("no %s runs to score", c)
		case len(untrusted) > 0 && !in.Gate.AllowUncalibratedJudge:
			chk.Pass, chk.Inconclusive = false, true
			chk.Summary = fmt.Sprintf("%s score %s not checked against %s: %s graded by a judge that is not calibrated", c, pts(got), pts(min), strings.Join(untrusted, ", "))
		case chk.Pass:
			chk.Summary = fmt.Sprintf("%s score %s, at least %s needed", c, pts(got), pts(min))
		default:
			chk.Summary = fmt.Sprintf("%s score %s is below the %s needed", c, pts(got), pts(min))
		}
		if len(untrusted) > 0 && in.Gate.AllowUncalibratedJudge && ok {
			chk.Notes = append(chk.Notes, "counted although the judge of "+strings.Join(untrusted, ", ")+" is not calibrated (allow_uncalibrated_judge)")
		}
		res.Checks = append(res.Checks, chk)
	}
	noBaseline := false
	if in.Gate.MaxRegression > 0 {
		if in.Baseline == nil || in.BaselineReport == nil {
			noBaseline = true
		} else {
			rin := in
			rin.Report = cand // Y07: the tasks both sides share when the benchmark changed
			res.Checks = append(res.Checks, regressionChecks(rin)...)
		}
	}
	var failed, unsure []string
	for _, c := range res.Checks {
		switch {
		case c.Inconclusive:
			unsure = append(unsure, c.Summary)
		case !c.Pass:
			failed = append(failed, c.Summary)
		}
	}
	switch {
	case len(failed) > 0:
		res.State = schemas.GateFail
		res.Summary = "Gate failed: " + strings.Join(failed, "; ")
	case len(unsure) > 0:
		res.State = schemas.GateInconclusive
		res.Summary = "Gate inconclusive: " + strings.Join(unsure, "; ")
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

// untrustedJudges names the judge-graded benchmarks of a capability ("all":
// every benchmark) whose judge verdicts may not use.
func untrustedJudges(r *Report, capability string) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, b := range r.Benchmarks {
		if capability != CapabilityAll && b.Capability != capability {
			continue
		}
		if b.Judge != nil && !b.Judge.Trusted {
			out = append(out, b.Benchmark+" ("+b.Judge.Label+")")
		}
	}
	return out
}

// pairing is the paired comparison of one benchmark between candidate and
// baseline: per-task differences over the tasks both ran.
type pairing struct {
	benchmark, capability string
	diffs                 []float64
	cand, base            float64 // mean success on the paired tasks
}

// pairBenchmarks pairs the candidate's benchmarks with the baseline's: the
// same benchmark at the same version, on the tasks both sides ran. notes say
// what was left out.
func pairBenchmarks(in GateInput) ([]pairing, []string) {
	var out []pairing
	var notes []string
	for _, cb := range in.Report.Benchmarks {
		ct := cb.tasks[in.Candidate]
		if len(ct) == 0 {
			continue
		}
		id, _, _ := strings.Cut(cb.Benchmark, "@")
		var bb *Benchmark
		for i := range in.BaselineReport.Benchmarks {
			x := &in.BaselineReport.Benchmarks[i]
			if xid, _, _ := strings.Cut(x.Benchmark, "@"); xid == id && len(x.tasks[in.BaselineArm]) > 0 {
				bb = x
				break
			}
		}
		if bb == nil {
			notes = append(notes, fmt.Sprintf("%s: the baseline did not run it", cb.Benchmark))
			continue
		}
		if cb.SuiteVersion != "" && bb.SuiteVersion != "" && cb.SuiteVersion != bb.SuiteVersion {
			notes = append(notes, fmt.Sprintf("%s: the baseline ran version %s, this evaluation %s, so it is not compared", id, bb.SuiteVersion, cb.SuiteVersion))
			continue
		}
		base := map[string]TaskTally{}
		for _, t := range bb.tasks[in.BaselineArm] {
			base[t.Key] = t
		}
		p := pairing{benchmark: cb.Benchmark, capability: cb.Capability}
		var sc, sb float64
		for _, t := range ct {
			b, ok := base[t.Key]
			if !ok || t.Runs == 0 || b.Runs == 0 {
				continue
			}
			c, w := float64(t.Succ)/float64(t.Runs), float64(b.Succ)/float64(b.Runs)
			p.diffs = append(p.diffs, c-w)
			sc, sb = sc+c, sb+w
		}
		if n := len(p.diffs); n > 0 {
			p.cand, p.base = sc/float64(n), sb/float64(n)
		}
		if extra := len(ct) - len(p.diffs); extra > 0 {
			notes = append(notes, fmt.Sprintf("%s: the baseline ran %d of these %d tasks; %d new tasks not compared", id, len(p.diffs), len(ct), extra))
		}
		if len(p.diffs) > 0 {
			out = append(out, p)
		}
	}
	return out, notes
}

// regressionChecks are the max_regression checks: one per capability with
// paired tasks, then the pooled "all".
func regressionChecks(in GateInput) []schemas.GateCheck {
	pairs, notes := pairBenchmarks(in)
	byCap := map[string][]pairing{}
	var capsSeen []string
	for _, p := range pairs {
		if _, ok := byCap[p.capability]; !ok {
			capsSeen = append(capsSeen, p.capability)
		}
		byCap[p.capability] = append(byCap[p.capability], p)
	}
	slices.Sort(capsSeen)
	var out []schemas.GateCheck
	for _, c := range capsSeen {
		out = append(out, regressionCheck(c, byCap[c], in.Gate.MaxRegression, capNotes(notes, in.Report, c)))
	}
	if len(pairs) > 0 {
		out = append(out, regressionCheck(CapabilityAll, pairs, in.Gate.MaxRegression, notes))
	} else {
		out = append(out, schemas.GateCheck{Capability: CapabilityAll, Rule: RuleMaxRegression, Threshold: in.Gate.MaxRegression, Inconclusive: true, Notes: notes,
			Summary: "no task ran on both sides at the same benchmark version, so the regression limit could not be checked"})
	}
	return out
}

// capNotes keeps the notes about the capability's benchmarks.
func capNotes(notes []string, r *Report, capability string) []string {
	var out []string
	for _, n := range notes {
		for _, b := range r.Benchmarks {
			id, _, _ := strings.Cut(b.Benchmark, "@")
			if b.Capability == capability && (strings.HasPrefix(n, b.Benchmark+":") || strings.HasPrefix(n, id+":")) {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// regressionCheck judges one capability's paired tasks against limit.
func regressionCheck(capability string, pairs []pairing, limit float64, notes []string) schemas.GateCheck {
	var diffs []float64
	var sc, sb float64
	for _, p := range pairs {
		diffs = append(diffs, p.diffs...)
		sc += p.cand * float64(len(p.diffs))
		sb += p.base * float64(len(p.diffs))
	}
	n := len(diffs)
	pc := verdictab.Paired(diffs)
	cand, base := sc/float64(n), sb/float64(n)
	chk := schemas.GateCheck{Capability: capability, Rule: RuleMaxRegression, Threshold: limit, Observed: cand, Baseline: &base, Tasks: n, Notes: notes}
	chk.Change = &pc.Delta
	if pc.OK {
		lo, hi := pc.Lo, pc.Hi
		chk.ChangeLow, chk.ChangeHigh = &lo, &hi
	}
	mdeText := ""
	if m := verdictab.MDE(n); !math.IsInf(m, 0) && !math.IsNaN(m) {
		chk.MDE = &m
		mdeText = fmt.Sprintf("; %d paired tasks can detect a change of about %s", n, pts(m))
		if m > limit {
			if need := verdictab.TasksFor(limit, 100000); need > 0 {
				mdeText += fmt.Sprintf(" (a %s limit needs about %d tasks)", pts(limit), need)
			}
		}
	}
	drop := -pc.Delta
	interval := ""
	if pc.OK {
		interval = fmt.Sprintf(" (95%% interval %s to %s)", signedPts(pc.Lo), signedPts(pc.Hi))
	}
	switch {
	case !pc.OK:
		chk.Inconclusive = true
		chk.Summary = fmt.Sprintf("%s: only %d paired task, too few to tell a drop from noise", capability, n)
	case drop > limit+1e-9 && pc.Hi < 0:
		chk.Summary = fmt.Sprintf("%s fell from %s to %s on %d paired tasks%s, a real drop larger than the %s allowed", capability, pts(base), pts(cand), n, interval, pts(limit))
	case drop > limit+1e-9:
		chk.Inconclusive = true
		chk.Summary = fmt.Sprintf("%s fell from %s to %s on %d paired tasks%s, over the %s allowed, but the drop could be noise%s", capability, pts(base), pts(cand), n, interval, pts(limit), mdeText)
	default:
		chk.Pass = true
		chk.Summary = fmt.Sprintf("%s %s, was %s on %d paired tasks%s (at most %s drop allowed)%s", capability, pts(cand), pts(base), n, interval, pts(limit), mdeText)
	}
	return chk
}

// signedPts is a change in points with its sign: "+3 pts", "-12 pts".
func signedPts(d float64) string {
	p := math.Round(d * 100)
	if p == 0 {
		return "0 pts"
	}
	return fmt.Sprintf("%+.0f pts", p)
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
