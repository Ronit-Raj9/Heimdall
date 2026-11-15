package metrics

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Names of the model turn family beside steps (docs/steps-and-evidence.md,
// "What steps does not count"). steps keeps its meaning, model turns that got
// an answer; these count what it leaves out, from the same gateway log.
const (
	ModelTurnsFailed = "model_turns_failed"
	ModelTurnsTotal  = "model_turns_total"
	ModelRetries     = "model_retries"
	ModelFallbacks   = "model_fallbacks"
)

var (
	_ schemas.Metric = (*modelTurnsFailedMetric)(nil)
	_ schemas.Metric = (*modelTurnsTotalMetric)(nil)
	_ schemas.Metric = (*modelRetriesMetric)(nil)
	_ schemas.Metric = (*modelFallbacksMetric)(nil)
)

// turnCounts splits the logical message requests of a complete gateway log
// into answered and failed ones. A logical request is answered when any of
// its rows (the original request or a fallback) returned a response, failed
// when every row finished without one. A request still processing makes the
// split unknown, the same rule steps follows.
func turnCounts(ev *schemas.Evidence) (answered, failed int, problem *schemas.Value) {
	if v, bad := requestLogProblem(ev.Requests); bad {
		return 0, 0, &v
	}
	for _, lr := range logicalMessageRequests(ev.Requests.Requests) {
		switch {
		case lr.succeeded:
			answered++
		case lr.processing:
			v := schemas.Unknown(fmt.Sprintf("message request %s still processing", lr.root), schemas.EvidenceGateway)
			return 0, 0, &v
		default:
			failed++
		}
	}
	return answered, failed, nil
}

// modelTurnsFailedMetric counts logical model requests whose final attempt
// failed: no row of the request, fallbacks included, returned a response.
type modelTurnsFailedMetric struct{}

// GetName implements schemas.Part.
func (m *modelTurnsFailedMetric) GetName() string { return ModelTurnsFailed }

// GetVersion implements schemas.Part.
func (m *modelTurnsFailedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *modelTurnsFailedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ModelTurnsFailed, Version: version, Label: "Model turns failed", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Logical model requests per run whose final attempt failed (every physical attempt, retries and fallbacks included, ended without a response; these are the turns steps leaves out, and steps plus this is model_turns_total), averaged over runs with a complete gateway log.",
	}
}

// Compute implements schemas.Metric.
func (m *modelTurnsFailedMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	_, failed, problem := turnCounts(orEmpty(ev))
	if problem != nil {
		return *problem, nil
	}
	return schemas.Observed(float64(failed), schemas.EvidenceGateway), nil
}

func newModelTurnsFailedMetric() *modelTurnsFailedMetric { return &modelTurnsFailedMetric{} }

// modelTurnsTotalMetric counts every finished logical model request, answered
// or failed.
type modelTurnsTotalMetric struct{}

// GetName implements schemas.Part.
func (m *modelTurnsTotalMetric) GetName() string { return ModelTurnsTotal }

// GetVersion implements schemas.Part.
func (m *modelTurnsTotalMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *modelTurnsTotalMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ModelTurnsTotal, Version: version, Label: "Model turns total (answered + failed)", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Logical model requests per run, answered or failed (steps plus model_turns_failed; the retries and fallbacks of one request count once here, and each one in model_attempts), averaged over runs with a complete gateway log.",
	}
}

// Compute implements schemas.Metric.
func (m *modelTurnsTotalMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	answered, failed, problem := turnCounts(orEmpty(ev))
	if problem != nil {
		return *problem, nil
	}
	return schemas.Observed(float64(answered+failed), schemas.EvidenceGateway), nil
}

func newModelTurnsTotalMetric() *modelTurnsTotalMetric { return &modelTurnsTotalMetric{} }

// modelRetriesMetric counts Bifrost retries: attempts after the first inside
// one gateway row's attempt trail.
type modelRetriesMetric struct{}

// GetName implements schemas.Part.
func (m *modelRetriesMetric) GetName() string { return ModelRetries }

// GetVersion implements schemas.Part.
func (m *modelRetriesMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *modelRetriesMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ModelRetries, Version: version, Label: "Model retries", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Gateway retries per run: attempts after the first in each message row's attempt trail, on the original request and on fallbacks alike (a retry the agent's SDK or CLI makes itself is a new logical request, not a retry here), averaged over runs with a complete gateway log.",
	}
}

// Compute implements schemas.Metric.
func (m *modelRetriesMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	n := 0
	for _, r := range messageRows(ev.Requests.Requests) {
		n += max(len(r.Attempts)-1, 0)
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

func newModelRetriesMetric() *modelRetriesMetric { return &modelRetriesMetric{} }

// modelFallbacksMetric counts fallback rows: every row of a logical request
// after its first.
type modelFallbacksMetric struct{}

// GetName implements schemas.Part.
func (m *modelFallbacksMetric) GetName() string { return ModelFallbacks }

// GetVersion implements schemas.Part.
func (m *modelFallbacksMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *modelFallbacksMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ModelFallbacks, Version: version, Label: "Model fallbacks", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Gateway fallbacks per run: message rows Bifrost created for a logical request after its first row, on another provider or model (model_attempts equals model_turns_total plus model_fallbacks plus model_retries), averaged over runs with a complete gateway log.",
	}
}

// Compute implements schemas.Metric.
func (m *modelFallbacksMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	n := 0
	for _, lr := range logicalMessageRequests(ev.Requests.Requests) {
		n += len(lr.rows) - 1
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

func newModelFallbacksMetric() *modelFallbacksMetric { return &modelFallbacksMetric{} }
