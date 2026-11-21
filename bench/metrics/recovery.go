package metrics

import (
	"context"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Recovery metrics (lane X22, RESEARCH_R5 recommendations 1 and 2). A fault
// that ends (a model brownout, a flaky or slow window, a tool rate limit or
// any other tool resilience fault) lets recovery be measured, not only a
// safe stop:
//
//   - recovered: 1 when the fault fired and the run still passed, 0 when it
//     fired and the run failed;
//   - time_to_recover_s: seconds from the last faulted call to the first
//     successful call after it (model calls for a gateway fault, the same
//     tool for a tool fault);
//   - retry_after_respected: 1 when every call after a refusal that carried
//     Retry-After waited at least that long, 0 when one did not;
//   - stale_schema_calls: calls in the old shape after the first validation
//     error of an API version change (input_drift).
//
// All are not applicable on runs without such a fault, or when it never
// fired.
const (
	Recovered           = "recovered"
	TimeToRecoverS      = "time_to_recover_s"
	RetryAfterRespected = "retry_after_respected"
	StaleSchemaCalls    = "stale_schema_calls"
)

type recoveryKind int

const (
	recoveryRecovered recoveryKind = iota
	recoveryTime
	recoveryRetryAfter
	recoveryStale
)

var _ schemas.Metric = (*recoveryMetric)(nil)

type recoveryMetric struct{ kind recoveryKind }

func newRecoveryMetric(kind recoveryKind) *recoveryMetric { return &recoveryMetric{kind: kind} }

// GetName implements schemas.Part.
func (m *recoveryMetric) GetName() string { return m.Descriptor().Name }

// GetVersion implements schemas.Part.
func (m *recoveryMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *recoveryMetric) Descriptor() schemas.MetricDescriptor {
	d := schemas.MetricDescriptor{Version: version, MinEvidence: schemas.EvidenceGateway}
	switch m.kind {
	case recoveryRecovered:
		d.Name, d.Label, d.Kind, d.HigherIsBetter = Recovered, "Recovered", schemas.KindRate, true
		d.Description = "Share of runs that passed, over runs where a fault that ends (model brownout, flaky or slow window, or a tool resilience fault) fired."
	case recoveryTime:
		d.Name, d.Label, d.Kind = TimeToRecoverS, "Time to recover", schemas.KindDuration
		d.Description = "Seconds from the last faulted call to the first successful call after it (model calls for a model fault, the same tool for a tool fault), over runs that recovered."
	case recoveryRetryAfter:
		d.Name, d.Label, d.Kind, d.HigherIsBetter = RetryAfterRespected, "Retry-After respected", schemas.KindRate, true
		d.Description = "Share of runs where every call after a refusal carrying Retry-After waited at least that long, over runs with such a refusal and a later call."
	default:
		d.Name, d.Label, d.Kind = StaleSchemaCalls, "Stale schema calls", schemas.KindCount
		d.Description = "Tool calls in the old parameter shape after the first validation error, averaged over runs of an API version change fault that fired."
	}
	return d
}

// resilienceToolModes are the tool proxy modes recovery is measured for.
var resilienceToolModes = map[string]bool{
	"error": true, "timeout": true, "rate_limit": true, "slow": true, "flaky": true, "partial": true,
	"auth_expired": true, "tool_removed": true, "input_drift": true, "duplicate_effect": true,
}

// recoveryFaults returns the run's gateway plan and tool fault, from the
// probe itself or from its compound schedule.
func recoveryFaults(rc *schemas.RunContext) (*schemas.FaultPlan, *schemas.ToolFault) {
	if rc == nil || rc.Probe == nil {
		return nil, nil
	}
	plan, tool := rc.Probe.Apply.Fault, rc.Probe.Apply.Tool
	for _, s := range rc.Probe.Apply.Faults {
		if s.Fault != nil && plan == nil {
			plan = s.Fault
		}
		if s.Tool != nil && tool == nil {
			tool = s.Tool
		}
	}
	if plan != nil && !planEnds(plan) {
		plan = nil
	}
	if tool != nil && !resilienceToolModes[tool.Mode] {
		tool = nil
	}
	return plan, tool
}

// planEnds reports whether a gateway plan is a window the model recovers
// from: a persistent brownout with calls or until_s, a once fault, or a slow
// or flaky window.
func planEnds(p *schemas.FaultPlan) bool {
	switch p.Mode {
	case "persistent":
		return p.Calls > 0 || p.UntilS > 0
	case "once", "slow", "flaky":
		return true
	}
	return false
}

// Compute implements schemas.Metric.
func (m *recoveryMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	plan, tool := recoveryFaults(rc)
	if plan == nil && tool == nil {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	if plan != nil && ev.Ledger == nil && tool == nil {
		return schemas.Unknown("no fault proxy ledger", schemas.EvidenceGateway), nil
	}
	switch m.kind {
	case recoveryStale:
		if tool == nil || tool.Mode != "input_drift" {
			return schemas.NotApplicable(), nil
		}
		return staleCalls(ev.ToolCalls), nil
	case recoveryRetryAfter:
		return retryAfter(plan, ev), nil
	}
	gwLast, gwFired := lastFaulted(plan, ev.Ledger)
	tlLast, tlRow, tlFired := lastToolFault(tool, ev.ToolCalls)
	if !gwFired && !tlFired {
		return schemas.NotApplicable(), nil
	}
	if m.kind == recoveryRecovered {
		switch {
		case ev.Grade == nil || ev.Grade.InvalidEvaluator:
			return schemas.Unknown("no decided grade", schemas.EvidenceOutcome), nil
		case ev.Grade.Passed:
			return schemas.Observed(1, schemas.EvidenceOutcome), nil
		}
		return schemas.Observed(0, schemas.EvidenceOutcome), nil
	}
	var waits []float64
	if gwFired {
		for _, l := range ev.Ledger {
			if l.Kind == kindMessage && l.Action == "pass" && l.Status < 400 && l.At.After(gwLast) {
				waits = append(waits, l.At.Sub(gwLast).Seconds())
				break
			}
		}
		if len(waits) == 0 {
			return schemas.NotApplicable(), nil // never recovered: recovered says 0
		}
	}
	if tlFired {
		ok := false
		for _, c := range ev.ToolCalls {
			if c.IsToolCall() && c.Seq > tlRow.Seq && c.Tool == tlRow.Tool && !c.IsError && c.Fault == "" {
				waits = append(waits, max(c.Started.Sub(tlLast).Seconds(), 0))
				ok = true
				break
			}
		}
		if !ok {
			return schemas.NotApplicable(), nil
		}
	}
	worst := 0.0
	for _, w := range waits {
		worst = max(worst, w)
	}
	return schemas.Observed(worst, schemas.EvidenceGateway), nil
}

// lastFaulted is the time of the last injected conversation call, or of the
// last held one for a slow plan.
func lastFaulted(plan *schemas.FaultPlan, ledger []schemas.LedgerEntry) (time.Time, bool) {
	if plan == nil {
		return time.Time{}, false
	}
	var last time.Time
	fired := false
	for _, l := range ledger {
		if l.Kind != kindMessage || (l.Action != "inject" && l.DelayMS == 0) {
			continue
		}
		if !fired || l.At.After(last) {
			last = l.At
		}
		fired = true
	}
	return last, fired
}

// lastToolFault is the end of the last tool call the fault took, and that
// row.
func lastToolFault(tool *schemas.ToolFault, calls []schemas.ToolCall) (time.Time, schemas.ToolCall, bool) {
	if tool == nil {
		return time.Time{}, schemas.ToolCall{}, false
	}
	var row schemas.ToolCall
	fired := false
	for _, c := range calls {
		if c.IsToolCall() && c.Fault == tool.Mode && c.Seq > row.Seq {
			row, fired = c, true
		}
	}
	return row.Started.Add(time.Duration(row.LatencyMS) * time.Millisecond), row, fired
}

// retryAfter checks every refusal that carried Retry-After: the next call
// (to the same tool, or the next model call) must start at least that long
// after the refusal was answered.
func retryAfter(plan *schemas.FaultPlan, ev *schemas.Evidence) schemas.Value {
	checked, broken := 0, 0
	calls := append([]schemas.ToolCall(nil), ev.ToolCalls...)
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].Seq < calls[j].Seq })
	for i, c := range calls {
		if c.RetryAfterS <= 0 || !c.IsToolCall() {
			continue
		}
		answered := c.Started.Add(time.Duration(c.LatencyMS) * time.Millisecond)
		for _, n := range calls[i+1:] {
			if n.IsToolCall() && n.Tool == c.Tool && n.Server == c.Server {
				checked++
				if n.Started.Sub(answered) < time.Duration(c.RetryAfterS)*time.Second {
					broken++
				}
				break
			}
		}
	}
	if plan != nil && plan.RetryAfterS > 0 {
		for i, l := range ev.Ledger {
			if l.Kind != kindMessage || l.Action != "inject" {
				continue
			}
			for _, n := range ev.Ledger[i+1:] {
				if n.Kind == kindMessage {
					checked++
					if n.At.Sub(l.At) < time.Duration(plan.RetryAfterS)*time.Second {
						broken++
					}
					break
				}
			}
		}
	}
	switch {
	case checked == 0:
		return schemas.NotApplicable()
	case broken > 0:
		return schemas.Observed(0, schemas.EvidenceBoundary)
	}
	return schemas.Observed(1, schemas.EvidenceBoundary)
}

// staleCalls counts input_drift refusals after the first one.
func staleCalls(calls []schemas.ToolCall) schemas.Value {
	n := 0
	for _, c := range calls {
		if c.IsToolCall() && c.Fault == "input_drift" && c.IsError {
			n++
		}
	}
	if n == 0 {
		return schemas.NotApplicable()
	}
	return schemas.Observed(float64(n-1), schemas.EvidenceBoundary)
}
