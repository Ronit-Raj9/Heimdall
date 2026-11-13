package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// WallTimeS is the name of the agent wall-clock duration.
const WallTimeS = "wall_time_s"

var _ schemas.Metric = (*wallTimeMetric)(nil)

// wallTimeMetric is the time from agent container start to exit, read from
// the engine's clock.
type wallTimeMetric struct{}

// GetName implements schemas.Part.
func (m *wallTimeMetric) GetName() string { return WallTimeS }

// GetVersion implements schemas.Part.
func (m *wallTimeMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *wallTimeMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           WallTimeS,
		Version:        version,
		Label:          "Wall time",
		Kind:           schemas.KindDuration,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Seconds from agent container start to exit per run, averaged over runs with a recorded start and end.",
		PS2:            true,
	}
}

// Compute implements schemas.Metric. The run context's exec result is the
// engine's record; the copy in evidence is read only when the context has
// none. A timed-out run still has a duration. A zero timestamp or an end
// before the start is a broken clock record, not a zero-second run.
func (m *wallTimeMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	var exec *schemas.ExecResult
	if rc != nil {
		exec = rc.Exec
	}
	if exec == nil && ev != nil {
		exec = ev.Exec
	}
	switch {
	case exec == nil:
		return schemas.Unknown("no exec result", schemas.EvidenceOutcome), nil
	case exec.Started.IsZero() || exec.Ended.IsZero():
		return schemas.Unknown("exec start or end time missing", schemas.EvidenceOutcome), nil
	case exec.Ended.Before(exec.Started):
		return schemas.Unknown("exec ended before it started", schemas.EvidenceOutcome), nil
	}
	return schemas.Observed(exec.Ended.Sub(exec.Started).Seconds(), schemas.EvidenceOutcome), nil
}

// newWallTimeMetric returns the wall_time_s metric.
func newWallTimeMetric() *wallTimeMetric {
	return &wallTimeMetric{}
}
