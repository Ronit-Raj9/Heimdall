package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the metrics read from the tool proxy's ledger.
const (
	ToolCallsProxied = "tool_calls_proxied"
	ToolErrorRate    = "tool_error_rate"
	ToolRecovery     = "tool_recovery"
	DistinctTools    = "distinct_tools"
)

// Reasons a tool ledger metric has no number.
const (
	reasonNoToolLedger = "tool ledger missing"
	reasonNoToolCalls  = "no proxied tool calls"
)

// toolsListTool is the tool name the proxy records for an MCP tools/list
// request: tool discovery, not a call, so the metrics skip it.
const toolsListTool = "tools/list"

// The tool ledger is written by the per-run tool proxy, a container the agent
// cannot reach, at the boundary between the agent and its tools: every value
// here is boundary-level evidence. A task without a proxied service has no
// ledger to read, so the values do not apply; a task with one whose ledger is
// missing has values that could not be observed, so they are unknown, never
// zero.

var (
	_ schemas.Metric = (*toolCallsProxiedMetric)(nil)
	_ schemas.Metric = (*toolErrorRateMetric)(nil)
	_ schemas.Metric = (*toolRecoveryMetric)(nil)
	_ schemas.Metric = (*distinctToolsMetric)(nil)
)

// toolCallsProxiedMetric counts the calls the agent made to proxied services.
type toolCallsProxiedMetric struct{}

// GetName implements schemas.Part.
func (m *toolCallsProxiedMetric) GetName() string { return ToolCallsProxied }

// GetVersion implements schemas.Part.
func (m *toolCallsProxiedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolCallsProxiedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ToolCallsProxied,
		Version:        version,
		Label:          "Proxied tool calls",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Calls the agent made to the run's proxied tool and MCP services, read from the tool proxy's ledger (tool listings excluded), averaged over runs of tasks with proxied services.",
	}
}

// Compute implements schemas.Metric.
func (m *toolCallsProxiedMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := proxiedCalls(rc, ev)
	if !ok {
		return v, nil
	}
	return schemas.Observed(float64(len(calls)), schemas.EvidenceBoundary), nil
}

// newToolCallsProxiedMetric returns the tool_calls_proxied metric.
func newToolCallsProxiedMetric() *toolCallsProxiedMetric { return &toolCallsProxiedMetric{} }

// toolErrorRateMetric is the share of proxied calls that failed, as the
// agent saw them: tool errors, protocol errors, unreachable servers and
// injected faults alike.
type toolErrorRateMetric struct{}

// GetName implements schemas.Part.
func (m *toolErrorRateMetric) GetName() string { return ToolErrorRate }

// GetVersion implements schemas.Part.
func (m *toolErrorRateMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolErrorRateMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ToolErrorRate,
		Version:        version,
		Label:          "Tool error rate",
		Kind:           schemas.KindRate,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Share of the run's proxied tool calls that failed (tool errors, protocol errors, unreachable servers and injected faults), averaged over runs that made at least one proxied call.",
	}
}

// Compute implements schemas.Metric. A run with no proxied call has no
// rate: not applicable.
func (m *toolErrorRateMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := proxiedCalls(rc, ev)
	if !ok {
		return v, nil
	}
	if len(calls) == 0 {
		return schemas.NotApplicable(), nil
	}
	failed := 0
	for _, c := range calls {
		if callFailed(c) {
			failed++
		}
	}
	return schemas.Observed(float64(failed)/float64(len(calls)), schemas.EvidenceBoundary), nil
}

// newToolErrorRateMetric returns the tool_error_rate metric.
func newToolErrorRateMetric() *toolErrorRateMetric { return &toolErrorRateMetric{} }

// toolRecoveryMetric is the share of failed proxied calls that the agent got
// past: a later call to the same server succeeded.
type toolRecoveryMetric struct{}

// GetName implements schemas.Part.
func (m *toolRecoveryMetric) GetName() string { return ToolRecovery }

// GetVersion implements schemas.Part.
func (m *toolRecoveryMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolRecoveryMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ToolRecovery,
		Version:        version,
		Label:          "Tool recovery",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Share of failed proxied tool calls followed by a later successful call to the same server, averaged over runs with at least one failed proxied call.",
	}
}

// Compute implements schemas.Metric. A run with no failed call had nothing
// to recover from: not applicable.
func (m *toolRecoveryMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := proxiedCalls(rc, ev)
	if !ok {
		return v, nil
	}
	failed, recovered := 0, 0
	for i, c := range calls {
		if !callFailed(c) {
			continue
		}
		failed++
		for _, later := range calls[i+1:] {
			if later.Server == c.Server && !callFailed(later) {
				recovered++
				break
			}
		}
	}
	if failed == 0 {
		return schemas.NotApplicable(), nil
	}
	return schemas.Observed(float64(recovered)/float64(failed), schemas.EvidenceBoundary), nil
}

// newToolRecoveryMetric returns the tool_recovery metric.
func newToolRecoveryMetric() *toolRecoveryMetric { return &toolRecoveryMetric{} }

// distinctToolsMetric counts the different tools the agent called.
type distinctToolsMetric struct{}

// GetName implements schemas.Part.
func (m *distinctToolsMetric) GetName() string { return DistinctTools }

// GetVersion implements schemas.Part.
func (m *distinctToolsMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric. Lower is better, like tool_calls:
// the outcome is judged by the verifier, and reaching it with fewer kinds of
// calls is the cheaper path; the number is a diagnostic, never a primary
// endpoint.
func (m *distinctToolsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           DistinctTools,
		Version:        version,
		Label:          "Distinct tools",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Different tools (server and tool name pairs) the agent called through the tool proxy, averaged over runs of tasks with proxied services.",
	}
}

// Compute implements schemas.Metric.
func (m *distinctToolsMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := proxiedCalls(rc, ev)
	if !ok {
		return v, nil
	}
	seen := map[[2]string]bool{}
	for _, c := range calls {
		seen[[2]string{c.Server, c.Tool}] = true
	}
	return schemas.Observed(float64(len(seen)), schemas.EvidenceBoundary), nil
}

// newDistinctToolsMetric returns the distinct_tools metric.
func newDistinctToolsMetric() *distinctToolsMetric { return &distinctToolsMetric{} }

// proxiedCalls returns the ledger's tool calls (schemas.ToolCall.IsToolCall),
// without tool listings or rows of another kind, or the
// value to report instead and false: not applicable for a task without a
// proxied service, unknown when the ledger is missing.
func proxiedCalls(rc *schemas.RunContext, ev *schemas.Evidence) ([]schemas.ToolCall, schemas.Value, bool) {
	if !hasProxiedService(rc) {
		return nil, schemas.NotApplicable(), false
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return nil, schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), false
	}
	calls := make([]schemas.ToolCall, 0, len(ev.ToolCalls))
	for _, c := range ev.ToolCalls {
		if c.Protocol == "mcp" && c.Tool == toolsListTool || !c.IsToolCall() {
			continue // listings, handshakes, notifications and opaque traffic are not tool calls
		}
		calls = append(calls, c)
	}
	return calls, schemas.Value{}, true
}

// callFailed reports whether the agent saw the call fail: an error answer,
// or no answer at all.
func callFailed(c schemas.ToolCall) bool {
	return c.IsError || c.Undelivered
}

// hasProxiedService reports whether the run's suite or task has a service
// the tool proxy fronts: one that says proxy, or by default a tool or mcp
// service, and never a hidden one.
func hasProxiedService(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	var specs []schemas.ServiceSpec
	if rc.Suite != nil {
		specs = append(specs, rc.Suite.Services...)
	}
	if rc.Task != nil {
		specs = append(specs, rc.Task.Services...)
	}
	for _, s := range specs {
		proxied := s.Role == schemas.ServiceRoleTool || s.Role == schemas.ServiceRoleMCP
		if s.Proxy != nil {
			proxied = *s.Proxy
		}
		if proxied && !s.Hidden {
			return true
		}
	}
	return false
}
