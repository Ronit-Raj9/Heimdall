package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// JudgeCostUSD is the name of the judge spend metric.
const JudgeCostUSD = "judge_cost_usd"

var _ schemas.Metric = (*judgeCostMetric)(nil)

// judgeCostMetric reports what the judge model's calls cost for one run's
// grade (Grade.Judge.USD), priced from the pinned snapshot. It is
// report-only and kept apart from cost_usd: the judge is part of measuring,
// not of the agent, so its spend never enters the agent's cost or the
// comparison between arms.
type judgeCostMetric struct{}

// GetName implements schemas.Part.
func (m *judgeCostMetric) GetName() string { return JudgeCostUSD }

// GetVersion implements schemas.Part.
func (m *judgeCostMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *judgeCostMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           JudgeCostUSD,
		Version:        version,
		Label:          "Judge cost (USD)",
		Kind:           schemas.KindUSD,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "US dollars the judge model's calls cost per run in a judged suite at pinned prices, report-only and never part of cost_usd, averaged over runs whose judge calls were all priced.",
		PS2:            false,
	}
}

// Compute implements schemas.Metric. Outside a judged suite it is not
// applicable. In one, a judged grade carries its own value (unknown when a
// call could not be priced); a grade decided without the judge (no answer
// to judge) cost nothing; a missing grade is unknown, since judge calls may
// have been made before grading failed.
func (m *judgeCostMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	g := orEmpty(ev).Grade
	judged := g != nil && g.Judged
	if !judged && (rc == nil || utils.Grading(rc.Suite) != schemas.GradingJudged) {
		return schemas.NotApplicable(), nil
	}
	switch {
	case g == nil:
		return schemas.Unknown(reasonNoGrade, schemas.EvidenceGateway), nil
	case g.Judge != nil:
		return g.Judge.USD, nil
	case judged:
		return schemas.Unknown("judged grade without a judge record", schemas.EvidenceGateway), nil
	}
	return schemas.Observed(0, schemas.EvidenceGateway), nil
}

// newJudgeCostMetric returns the judge_cost_usd metric.
func newJudgeCostMetric() *judgeCostMetric {
	return &judgeCostMetric{}
}
