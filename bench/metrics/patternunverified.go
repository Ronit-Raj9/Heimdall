package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// PatternUnverifiedFinish is the name of the success-claim-without-tests
// pattern.
const PatternUnverifiedFinish = "pattern_unverified_finish"

var _ schemas.Metric = (*patternUnverifiedMetric)(nil)

// patternUnverifiedMetric flags a run that claims success without running
// tests after its last edit. It says nothing about whether the work passed:
// false_completion answers that against the hidden verifier.
type patternUnverifiedMetric struct{}

// GetName implements schemas.Part.
func (m *patternUnverifiedMetric) GetName() string { return PatternUnverifiedFinish }

// GetVersion implements schemas.Part.
func (m *patternUnverifiedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *patternUnverifiedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           PatternUnverifiedFinish,
		Version:        version,
		Label:          "Unverified finish",
		Kind:           schemas.KindRate,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description: "Share of runs that end with a success claim and no shell command containing pytest, py.test, " +
			"python -m pytest, unittest, npm test, go test or make test (or a tool named for tests) after the last edit tool call, " +
			"over runs with gateway or agent_stream events and a known claim; an observed pattern, not a root cause.",
		PS2: false,
	}
}

// Compute implements schemas.Metric. Without an edit, a test run anywhere in
// the run counts as verification. An incomplete gateway with no stream
// cannot rule out a test run it did not log, so that case is unknown.
func (m *patternUnverifiedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	tr, ok := pickTrace(ev.Events)
	if !ok {
		return schemas.Unknown(reasonNoEvents, schemas.EvidenceGateway), nil
	}
	if ev.Claim == nil {
		return schemas.Unknown(reasonNoClaim, tr.level), nil
	}
	if !ev.Claim.Made {
		return patternValue(false, tr.level, nil, reasonNoClaimMade), nil
	}
	lastEdit := -1
	for i, c := range tr.calls {
		if isEdit(c) {
			lastEdit = i
		}
	}
	for _, c := range tr.calls[lastEdit+1:] {
		if runsTests(c) {
			return patternValue(false, tr.level, nil, fmt.Sprintf("tests ran after the last edit: %s (%s)", describeCall(c), c.EventID)), nil
		}
	}
	if tr.incomplete {
		return schemas.Unknown(gatewayIncomplete+" and no agent_stream events", tr.level), nil
	}
	var ids []string
	summary := "success claimed with no test command in the run"
	if lastEdit >= 0 {
		edit := tr.calls[lastEdit]
		ids = append(ids, edit.EventID)
		summary = fmt.Sprintf("success claimed with no test command after the last edit, %s", describeCall(edit))
	}
	if tr.finish != "" && !(len(ids) > 0 && ids[0] == tr.finish) {
		ids = append(ids, tr.finish)
	}
	return patternValue(true, tr.level, ids, summary), nil
}

// newPatternUnverifiedMetric returns the pattern_unverified_finish metric.
func newPatternUnverifiedMetric() *patternUnverifiedMetric {
	return &patternUnverifiedMetric{}
}
