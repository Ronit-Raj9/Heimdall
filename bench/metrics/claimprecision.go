package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ClaimPrecision is the name of the supported-claim rate.
const ClaimPrecision = "claim_precision"

var _ schemas.Metric = (*claimPrecisionMetric)(nil)

// claimPrecisionMetric asks how often a success claim was true. It applies
// only to runs that made a claim, so its mean over runs is the share of claims
// the hidden verifier confirmed.
type claimPrecisionMetric struct{}

// GetName implements schemas.Part.
func (m *claimPrecisionMetric) GetName() string { return ClaimPrecision }

// GetVersion implements schemas.Part.
func (m *claimPrecisionMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *claimPrecisionMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ClaimPrecision,
		Version:        version,
		Label:          "Claim precision",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Share of success claims the hidden verifier confirmed, over runs that made a success claim and have a decided grade.",
		PS2:            false,
	}
}

// Compute implements schemas.Metric. Like false_completion it compares the
// claim with the grade and ignores veto gates.
func (m *claimPrecisionMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if ev.Claim == nil {
		return schemas.Unknown(reasonNoClaim, schemas.EvidenceOutcome), nil
	}
	if !ev.Claim.Made {
		return schemas.NotApplicable(), nil
	}
	if v, bad := gradeProblem(ev.Grade); bad {
		return v, nil
	}
	if ev.Grade.Passed {
		return schemas.Observed(1, claimLevel(ev.Claim)), nil
	}
	return schemas.Observed(0, claimLevel(ev.Claim)), nil
}

// newClaimPrecisionMetric returns the claim_precision metric.
func newClaimPrecisionMetric() *claimPrecisionMetric {
	return &claimPrecisionMetric{}
}
