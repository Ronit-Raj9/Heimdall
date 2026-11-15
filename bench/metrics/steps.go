package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Steps is the name of the model-turn count.
const Steps = "steps"

var _ schemas.Metric = (*stepsMetric)(nil)

// stepsMetric counts model turns: logical message requests that got a
// response from the model.
type stepsMetric struct{}

// GetName implements schemas.Part.
func (m *stepsMetric) GetName() string { return Steps }

// GetVersion implements schemas.Part.
func (m *stepsMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *stepsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           Steps,
		Version:        version,
		Label:          "Steps (model turns answered)",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "Model turns that got an answer per run (logical message requests answered by the model, retries and fallbacks of one request counted once; not everything the agent did: failed turns are model_turns_failed, physical retries and fallbacks model_attempts, commands, file changes and tool calls between turns agent_steps), averaged over runs with a complete gateway log.",
		PS2:            true,
	}
}

// Compute implements schemas.Metric. Bifrost retries live inside one row as
// attempts, and a fallback is a child row of the original request, so both
// collapse into one logical request. A logical request that only ever failed
// is not a turn: the model never answered, and the SDK or CLI retry that
// follows it is a new request that would otherwise count twice under an
// outage probe.
func (m *stepsMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	n := 0
	for _, lr := range logicalMessageRequests(ev.Requests.Requests) {
		switch {
		case lr.succeeded:
			n++
		case lr.processing:
			return schemas.Unknown(fmt.Sprintf("message request %s still processing", lr.root), schemas.EvidenceGateway), nil
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

// newStepsMetric returns the steps metric.
func newStepsMetric() *stepsMetric {
	return &stepsMetric{}
}
