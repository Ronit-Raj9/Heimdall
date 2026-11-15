package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the agent-reported metrics of a remote agent (Remote coverage,
// docs/remote-execution.md). They hold what a remote_http deployment said
// about its own usage in its response (Evidence.Remote.Reported). They are
// separate from tokens and cost_usd on purpose: those stay trusted gateway
// evidence, unknown with the external traffic reason when the deployment's
// model calls do not go through this gateway, and a self-report never fills
// them.
const (
	ReportedTokens  = "reported_tokens"
	ReportedCostUSD = "reported_cost_usd"
)

// reportedSource labels every observed value of these metrics as the
// agent's own word.
const reportedSource schemas.EvidenceOrigin = "agent_reported"

const (
	reasonNoRemoteReport = "the remote agent reported no usage in its response"
	reasonNoReportedCost = "the remote agent reported no cost in its response"
)

var (
	_ schemas.Metric = (*reportedTokensMetric)(nil)
	_ schemas.Metric = (*reportedCostMetric)(nil)
)

// reportedTokensMetric is the token count a remote agent reported.
type reportedTokensMetric struct{}

// GetName implements schemas.Part.
func (m *reportedTokensMetric) GetName() string { return ReportedTokens }

// GetVersion implements schemas.Part.
func (m *reportedTokensMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *reportedTokensMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ReportedTokens, Version: version, Label: "Reported tokens (agent-reported)", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
		Description: "Tokens a remote agent says it used (total_tokens, else input plus output), from its own response: agent-reported, never verified and never part of tokens, averaged over runs of remote agents that reported usage.",
	}
}

// Compute implements schemas.Metric. Not applicable to an agent that is not
// remote; unknown when the remote agent reported no usage.
func (m *reportedTokensMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	rep, v, ok := remoteReport(rc, ev)
	if !ok {
		return v, nil
	}
	switch {
	case rep.TotalTokens != nil:
		return reported(float64(*rep.TotalTokens)), nil
	case rep.InputTokens != nil || rep.OutputTokens != nil:
		var n int64
		for _, p := range []*int64{rep.InputTokens, rep.OutputTokens} {
			if p != nil {
				n += *p
			}
		}
		return reported(float64(n)), nil
	}
	return schemas.Unknown(reasonNoRemoteReport, schemas.EvidenceNative), nil
}

// newReportedTokensMetric returns the reported_tokens metric.
func newReportedTokensMetric() *reportedTokensMetric { return &reportedTokensMetric{} }

// reportedCostMetric is the cost a remote agent reported.
type reportedCostMetric struct{}

// GetName implements schemas.Part.
func (m *reportedCostMetric) GetName() string { return ReportedCostUSD }

// GetVersion implements schemas.Part.
func (m *reportedCostMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *reportedCostMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ReportedCostUSD, Version: version, Label: "Reported cost (agent-reported)", Kind: schemas.KindUSD,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceNative,
		Description: "Cost in USD a remote agent says it incurred, from its own response: agent-reported, never verified, never part of cost_usd or the spend cap, averaged over runs of remote agents that reported a cost.",
	}
}

// Compute implements schemas.Metric.
func (m *reportedCostMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	rep, v, ok := remoteReport(rc, ev)
	if !ok {
		return v, nil
	}
	if rep.CostUSD == nil {
		return schemas.Unknown(reasonNoReportedCost, schemas.EvidenceNative), nil
	}
	return reported(*rep.CostUSD), nil
}

// newReportedCostMetric returns the reported_cost_usd metric.
func newReportedCostMetric() *reportedCostMetric { return &reportedCostMetric{} }

// remoteReport returns the run's self-report, or the value to report when
// there is none: not applicable for an agent that is not remote, unknown for
// a remote agent that reported nothing.
func remoteReport(rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.RemoteReport, schemas.Value, bool) {
	if rc == nil || rc.Agent == nil || rc.Agent.Remote == nil {
		return nil, schemas.NotApplicable(), false
	}
	if ev == nil || ev.Remote == nil || ev.Remote.Reported == nil {
		return nil, schemas.Unknown(reasonNoRemoteReport, schemas.EvidenceNative), false
	}
	return ev.Remote.Reported, schemas.Value{}, true
}

// reported is an observed value labelled agent_reported.
func reported(v float64) schemas.Value {
	out := schemas.Observed(v, schemas.EvidenceNative)
	out.Source = reportedSource
	return out
}
