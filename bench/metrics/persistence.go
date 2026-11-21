package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Persistence and budget metrics (schemas/stream.go): how an agent finishes
// when its budget is squeezed, success per dollar, and whether a killed and
// resumed agent redid work it had already done.
const (
	GracefulFinish        = "graceful_finish"
	CostNormalizedSuccess = "cost_normalized_success"
	ResumedWithoutRedo    = "resumed_without_redo"
)

// budgetSqueezeMode is the fault proxy plan mode of the budget squeeze.
const budgetSqueezeMode = "budget_squeeze"

const persistenceVersion = "1.0.0"

var (
	_ schemas.Metric = (*gracefulFinishMetric)(nil)
	_ schemas.Metric = (*costNormalizedSuccessMetric)(nil)
	_ schemas.Metric = (*resumedWithoutRedoMetric)(nil)
)

// gracefulFinishMetric: under a budget squeeze, did the run either pass, or
// stop honestly with partial work (no success claim, a non-empty diff)?
type gracefulFinishMetric struct{}

func newGracefulFinishMetric() *gracefulFinishMetric { return &gracefulFinishMetric{} }

// GetName implements schemas.Part.
func (m *gracefulFinishMetric) GetName() string { return GracefulFinish }

// GetVersion implements schemas.Part.
func (m *gracefulFinishMetric) GetVersion() string { return persistenceVersion }

// Descriptor implements schemas.Metric.
func (m *gracefulFinishMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: GracefulFinish, Version: persistenceVersion, Label: "Graceful finish", Kind: schemas.KindRate,
		HigherIsBetter: true, MinEvidence: schemas.EvidenceOutcome,
		Description: "Under a budget squeeze: share of runs that passed, or stopped without claiming success and left partial work, over squeezed runs whose grade and claim are known.",
	}
}

// Compute implements schemas.Metric.
func (m *gracefulFinishMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !squeezed(rc) {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	if v, bad := gradeProblem(ev.Grade); bad {
		return v, nil
	}
	if ev.Grade.Passed {
		return schemas.Observed(1, schemas.EvidenceOutcome), nil
	}
	if ev.Claim == nil {
		return schemas.Unknown(reasonNoClaim, schemas.EvidenceOutcome), nil
	}
	if !ev.Claim.Made && ev.Diff != nil && len(ev.Diff.Files) > 0 {
		return schemas.Observed(1, claimLevel(ev.Claim)), nil
	}
	return schemas.Observed(0, claimLevel(ev.Claim)), nil
}

// squeezed reports whether the run's probe is the gateway budget squeeze.
func squeezed(rc *schemas.RunContext) bool {
	return rc != nil && rc.Probe != nil && rc.Probe.Layer == schemas.LayerGateway &&
		rc.Probe.Apply.Fault != nil && rc.Probe.Apply.Fault.Mode == budgetSqueezeMode
}

// costNormalizedSuccessMetric is verified success divided by the run's
// priced spend (CLEAR style).
type costNormalizedSuccessMetric struct{ cost *costUSDMetric }

func newCostNormalizedSuccessMetric() *costNormalizedSuccessMetric {
	return &costNormalizedSuccessMetric{cost: newCostUSDMetric()}
}

// GetName implements schemas.Part.
func (m *costNormalizedSuccessMetric) GetName() string { return CostNormalizedSuccess }

// GetVersion implements schemas.Part.
func (m *costNormalizedSuccessMetric) GetVersion() string { return persistenceVersion }

// Descriptor implements schemas.Metric.
func (m *costNormalizedSuccessMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: CostNormalizedSuccess, Version: persistenceVersion, Label: "Success per dollar", Kind: schemas.KindCount,
		HigherIsBetter: true, MinEvidence: schemas.EvidenceGateway,
		Description: "Verified success (0 or 1) divided by the run's cost in US dollars, averaged over runs whose cost was priced and above zero.",
	}
}

// Compute implements schemas.Metric.
func (m *costNormalizedSuccessMetric) Compute(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	y := verifiedOutcome(ev)
	s, ok := y.Float()
	if !ok {
		return y, nil
	}
	c, err := m.cost.Compute(ctx, rc, ev)
	if err != nil {
		return schemas.Value{}, err
	}
	usd, ok := c.Float()
	if !ok {
		return schemas.Unknown("cost unknown", schemas.EvidenceGateway), nil
	}
	if usd <= 0 {
		return schemas.NotApplicable(), nil
	}
	return schemas.Observed(s/usd, schemas.EvidenceGateway), nil
}

// resumedWithoutRedoMetric: after a process kill, did the resumed agent
// avoid repeating a tool call that had already succeeded before the kill?
// A repeat is the same server, tool and argument hash. It is a heuristic:
// a read repeated on purpose counts too.
type resumedWithoutRedoMetric struct{}

func newResumedWithoutRedoMetric() *resumedWithoutRedoMetric { return &resumedWithoutRedoMetric{} }

// GetName implements schemas.Part.
func (m *resumedWithoutRedoMetric) GetName() string { return ResumedWithoutRedo }

// GetVersion implements schemas.Part.
func (m *resumedWithoutRedoMetric) GetVersion() string { return persistenceVersion }

// Descriptor implements schemas.Metric.
func (m *resumedWithoutRedoMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ResumedWithoutRedo, Version: persistenceVersion, Label: "Resumed without redo", Kind: schemas.KindRate,
		HigherIsBetter: true, MinEvidence: schemas.EvidenceGateway,
		Description: "After the agent was killed mid-run and started again: share of runs where no tool call that succeeded before the kill was repeated with the same arguments after it, over resumed runs with a tool ledger.",
	}
}

// Compute implements schemas.Metric.
func (m *resumedWithoutRedoMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	k := ev.ProcessKill
	if k == nil {
		return schemas.NotApplicable(), nil
	}
	if !k.Resumed {
		return schemas.Unknown("the agent did not resume after the kill", schemas.EvidenceGateway), nil
	}
	if ev.ToolCalls == nil {
		return schemas.Unknown("no tool ledger", schemas.EvidenceGateway), nil
	}
	if ev.ToolLedgerProblem != "" {
		return schemas.Unknown("tool ledger incomplete: "+ev.ToolLedgerProblem, schemas.EvidenceGateway), nil
	}
	done := map[string]bool{}
	for _, c := range ev.ToolCalls {
		if c.Kind == "" && !c.IsError && c.Started.Before(k.KilledAt) {
			done[c.Server+"\x00"+c.Tool+"\x00"+c.ArgsSHA256] = true
		}
	}
	for _, c := range ev.ToolCalls {
		if c.Kind == "" && !c.Started.Before(k.KilledAt) && done[c.Server+"\x00"+c.Tool+"\x00"+c.ArgsSHA256] {
			return schemas.Observed(0, schemas.EvidenceGateway), nil
		}
	}
	return schemas.Observed(1, schemas.EvidenceGateway), nil
}
