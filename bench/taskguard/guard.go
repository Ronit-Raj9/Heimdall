package taskguard

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Check names (the admission record shows them on the readiness view).
const (
	CheckNoop        = "guard_noop_fails"
	CheckControl     = "guard_control_fails"
	CheckLeak        = "guard_no_answer_leak"
	CheckFlaky       = "guard_not_flaky"
	CheckDeterminism = "guard_deterministic"
	CheckClarity     = "guard_instruction_clear"
	CheckProvenance  = "guard_provenance"
	CheckPrivacy     = "guard_privacy"
)

// Checks lists the eight guard checks in the order Apply appends them.
func Checks() []string {
	return []string{CheckNoop, CheckControl, CheckLeak, CheckFlaky, CheckDeterminism, CheckClarity, CheckProvenance, CheckPrivacy}
}

// Generated says which builder made t, or ok false for every other task
// (hand-written and imported tasks are never guarded).
func Generated(t *schemas.Task) (builder string, ok bool) {
	if t == nil || t.Provenance == nil {
		return "", false
	}
	by := strings.TrimSpace(t.Provenance.CreatedBy)
	switch {
	case by == "task from-trace" || by == "task from-session":
		return BuilderTrace, true
	case by == "heimdall issuebench emit":
		return BuilderIssue, true
	case by == "api policy generator":
		return BuilderAPI, true
	case strings.HasPrefix(by, "policy generator"):
		return BuilderPolicy, true
	}
	return "", false
}

// Apply appends the guard checks to rec for a generated task and sets
// rec.Admitted false when a blocking check failed. It reads only files of
// t.Dir and the checks already in rec; it runs nothing and calls no model.
// For a task Generated says no, it returns rec unchanged.
func Apply(s *schemas.Suite, t *schemas.Task, rec *schemas.AdmissionRecord) *schemas.AdmissionRecord {
	builder, ok := Generated(t)
	if !ok || rec == nil {
		return rec
	}
	g, err := Read(t.Dir)
	if err != nil || g == nil {
		g = &Record{Builder: builder}
	}
	if len(g.LeakTexts) == 0 {
		g.LeakTexts = derivedLeakTexts(t)
	}
	c := &checker{s: s, t: t, rec: rec, g: g, builder: builder}
	checks := []schemas.AdmissionCheck{
		c.noop(), c.control(), c.leak(), c.flaky(), c.determinism(), c.clarity(), c.provenance(), c.privacy(),
	}
	for _, ch := range checks {
		rec.Checks = append(rec.Checks, ch)
		if !ch.Passed {
			rec.Admitted = false
		}
	}
	return rec
}
