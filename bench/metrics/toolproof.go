package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the tool proof metrics, read from the proof class of every
// requested tool call on the step timeline (docs/steps-and-evidence.md,
// "Proof classes").
const (
	ToolProofRate           = "tool_proof_rate"
	ToolProofProxy          = "tool_proof_proxy"
	ToolProofProcess        = "tool_proof_process"
	ToolProofFileEffect     = "tool_proof_file_effect"
	ToolProofShim           = "tool_proof_shim"
	ToolProofResultReturned = "tool_proof_result_returned"
	ToolProofNone           = "tool_proof_none"
)

// Reasons a tool proof metric has no number.
const (
	reasonNoRequestedCalls = "the model requested no tool calls"
	reasonNoProofClass     = "requested tool calls carry no proof class (timeline recorded before proof classes)"
	reasonNoTrustedWitness = "no trusted witness could see a call: no process or workspace observer ran and there is no tool proxy ledger"
)

// toolProofMetric is the tool proof rate (classes nil: the share of
// requested calls whose class is trusted) or the share of one proof class.
type toolProofMetric struct {
	name, label, description string
	classes                  map[string]bool // the classes counted in the numerator
	higherIsBetter           bool
	needsTrusted             bool // unknown when no trusted witness could exist
}

var _ schemas.Metric = (*toolProofMetric)(nil)

// GetName implements schemas.Part.
func (m *toolProofMetric) GetName() string { return m.name }

// GetVersion implements schemas.Part.
func (m *toolProofMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolProofMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: m.name, Version: version, Label: m.label, Kind: schemas.KindRate,
		HigherIsBetter: m.higherIsBetter, MinEvidence: schemas.EvidenceBoundary,
		Description: m.description,
	}
}

// Compute implements schemas.Metric. Every requested call has exactly one
// class (its strongest witness), so the six shares of a run add up to 1.
func (m *toolProofMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	if ev.Steps == nil {
		return schemas.Unknown(reasonNoTimeline, schemas.EvidenceBoundary), nil
	}
	total, hit := 0, 0
	for _, s := range ev.Steps {
		if s.Kind != schemas.StepToolRequest {
			continue
		}
		if s.ProofClass == "" {
			return schemas.Unknown(reasonNoProofClass, schemas.EvidenceBoundary), nil
		}
		total++
		if m.classes[s.ProofClass] {
			hit++
		}
	}
	if total == 0 {
		return schemas.Unknown(reasonNoRequestedCalls, schemas.EvidenceBoundary), nil
	}
	if m.needsTrusted && !trustedWitnessAvailable(ev) {
		return schemas.Unknown(reasonNoTrustedWitness, schemas.EvidenceBoundary), nil
	}
	v := schemas.Observed(float64(hit)/float64(total), schemas.EvidenceBoundary)
	v.Source, v.EvidenceLevel = schemas.EvidenceSourceInferred, schemas.EvidenceStrong
	return v, nil
}

// trustedWitnessAvailable reports whether a witness outside the agent's
// container could have seen a call run: an observer that ran, or a tool
// proxy ledger.
func trustedWitnessAvailable(ev *schemas.Evidence) bool {
	return observed(ev.Observers) || ev.ToolCalls != nil
}

// newToolProofMetrics returns tool_proof_rate and the six per-class shares.
func newToolProofMetrics() []schemas.Metric {
	share := func(name, label, class, what string, higher bool) schemas.Metric {
		return &toolProofMetric{
			name: name, label: label, classes: map[string]bool{class: true}, higherIsBetter: higher,
			description: fmt.Sprintf("Share of the tool calls the model requested whose strongest execution witness is %s (proof class %s; each call has exactly one class, in the order tool proxy, process observer, workspace observer, shim, result returned, none), averaged over runs with a complete gateway log, a step timeline and at least one requested call.", what, class),
		}
	}
	return []schemas.Metric{
		&toolProofMetric{
			name: ToolProofRate, label: "Tool proof rate (trusted witness)", higherIsBetter: true, needsTrusted: true,
			classes:     map[string]bool{schemas.ProofProxy: true, schemas.ProofProcess: true, schemas.ProofFileEffect: true},
			description: "Share of the tool calls the model requested that a witness outside the agent's container saw run (a tool proxy ledger row, a command tree the process observer saw, or a workspace change on the requested path; shim calls, results returned to the model and the agent's own report do not count), averaged over runs with a complete gateway log, a step timeline, at least one requested call and at least one trusted witness available (an observer that ran, or a tool proxy ledger).",
		},
		share(ToolProofProxy, "Tool proof: tool proxy", schemas.ProofProxy, "a tool proxy ledger row", true),
		share(ToolProofProcess, "Tool proof: process observer", schemas.ProofProcess, "a command tree the process observer saw", true),
		share(ToolProofFileEffect, "Tool proof: file effect", schemas.ProofFileEffect, "a workspace change on the requested path", true),
		share(ToolProofShim, "Tool proof: shim only", schemas.ProofShim, "a tool shim call, recorded inside the agent's container", true),
		share(ToolProofResultReturned, "Tool proof: result returned only", schemas.ProofResultReturned, "a result returned to the model (a later model request carried it, or the agent's stream reported it), which shows a result reached the model, not that the tool ran", false),
		share(ToolProofNone, "Tool proof: no witness", schemas.ProofNone, "nothing at all", false),
	}
}

// newToolProofMetric returns a fresh instance of the named tool proof metric.
func newToolProofMetric(name string) schemas.Metric {
	for _, m := range newToolProofMetrics() {
		if m.GetName() == name {
			return m
		}
	}
	panic("unknown tool proof metric " + name)
}
