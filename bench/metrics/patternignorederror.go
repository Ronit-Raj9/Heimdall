package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// PatternIgnoredError is the name of the ignored-tool-error pattern.
const PatternIgnoredError = "pattern_ignored_error"

// ignoredWindow is how many tool calls after a failed result may respond to
// it before the error counts as ignored.
const ignoredWindow = 2

var _ schemas.Metric = (*patternIgnoredErrorMetric)(nil)

// patternIgnoredErrorMetric flags a run that claims success after a tool
// error it did not respond to. Only the agent's own stream carries tool
// outcomes: Bifrost logs which results went back to the model, not whether
// they failed, so gateway-only evidence leaves this unknown.
type patternIgnoredErrorMetric struct{}

// GetName implements schemas.Part.
func (m *patternIgnoredErrorMetric) GetName() string { return PatternIgnoredError }

// GetVersion implements schemas.Part.
func (m *patternIgnoredErrorMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *patternIgnoredErrorMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           PatternIgnoredError,
		Version:        version,
		Label:          "Ignored error",
		Kind:           schemas.KindRate,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceNative,
		Description: "Share of runs that claim success after a failed tool result (an error flag or a non-zero exit) " +
			"that none of the next 2 tool calls answers with a retry of the same call or target, an edit, or a read of a file the failed call named, " +
			"over runs with agent_stream events and a known claim; an observed pattern, not a root cause.",
		PS2: false,
	}
}

// Compute implements schemas.Metric.
func (m *patternIgnoredErrorMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	tr, ok := traceOf(ev.Events, schemas.SourceAgentStream)
	if !ok {
		return schemas.Unknown(reasonNoStreamEvents, schemas.EvidenceNative), nil
	}
	if ev.Claim == nil {
		return schemas.Unknown(reasonNoClaim, tr.level), nil
	}
	if !ev.Claim.Made {
		return patternValue(false, tr.level, nil, reasonNoClaimMade), nil
	}
	failed, ignored := 0, 0
	var ids []string
	var first schemas.Event
	for i, e := range tr.events {
		if e.Type != schemas.EventToolResult || !failedResult(e) {
			continue
		}
		failed++
		call, hasCall := tr.callOf(e)
		if !hasCall {
			call = e // the result carries the call's name and target
		}
		next := nextCalls(tr.events[i+1:], ignoredWindow)
		if responds(call, hasCall, next) {
			continue
		}
		if ignored == 0 {
			first = e
		}
		ignored++
		ids = append(ids, e.EventID)
		for _, n := range next {
			ids = append(ids, n.EventID)
		}
	}
	if ignored == 0 {
		return patternValue(false, tr.level, nil, fmt.Sprintf("%d failed tool results, each answered within %d calls", failed, ignoredWindow)), nil
	}
	summary := fmt.Sprintf("success claimed after %d of %d failed tool results with no retry, edit or read within %d calls; first: %s",
		ignored, failed, ignoredWindow, describeCall(first))
	if s := oneLineText(first.Summary); s != "" {
		summary += fmt.Sprintf(": %q", clipText(s, maxReasonText))
	}
	return patternValue(true, tr.level, ids, summary), nil
}

// newPatternIgnoredErrorMetric returns the pattern_ignored_error metric.
func newPatternIgnoredErrorMetric() *patternIgnoredErrorMetric {
	return &patternIgnoredErrorMetric{}
}

// nextCalls returns up to n tool calls from events, in order.
func nextCalls(events []schemas.Event, n int) []schemas.Event {
	var out []schemas.Event
	for _, e := range events {
		if len(out) == n {
			break
		}
		if e.Type == schemas.EventToolCall {
			out = append(out, e)
		}
	}
	return out
}

// responds reports whether any of next answers the failed call: the same
// call again (by key, only when the call itself was found), the same
// non-empty target, any edit, or a read of a file the failed call named.
func responds(failed schemas.Event, hasCall bool, next []schemas.Event) bool {
	for _, n := range next {
		switch {
		case hasCall && callKey(n) == callKey(failed):
			return true
		case failed.Target != "" && n.Target == failed.Target:
			return true
		case isEdit(n):
			return true
		case isRead(n) && readsFileOf(n, failed.Target):
			return true
		}
	}
	return false
}
