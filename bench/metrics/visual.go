package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X1 vision grading metrics (docs/visual-grading.md). They read the visual
// grade (Grade.Visual) and apply only to runs graded by grader visual.
const (
	VisualScore      = "visual_score"
	VisualJudgeScore = "visual_judge_score"
)

var (
	_ schemas.Metric = (*visualScoreMetric)(nil)
	_ schemas.Metric = (*visualJudgeScoreMetric)(nil)
)

// visualScoreMetric is the share of a run's deterministic visual checks
// that passed.
type visualScoreMetric struct{}

func newVisualScoreMetric() *visualScoreMetric { return &visualScoreMetric{} }

// GetName implements schemas.Part.
func (m *visualScoreMetric) GetName() string { return VisualScore }

// GetVersion implements schemas.Part.
func (m *visualScoreMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *visualScoreMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           VisualScore,
		Version:        version,
		Label:          "Visual checks passed",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Share of the task's deterministic visual checks (dimensions, pixel, SSIM, perceptual hash, colours, region) that passed on the image the bench captured, averaged over runs of tasks with output.visual.",
	}
}

// Compute implements schemas.Metric.
func (m *visualScoreMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !visualRun(rc) {
		return schemas.NotApplicable(), nil
	}
	g := orEmpty(ev).Grade
	switch {
	case g == nil:
		return schemas.Unknown("run not graded", schemas.EvidenceOutcome), nil
	case g.Visual == nil || g.Visual.Score == nil:
		return schemas.Unknown("no visual checks ran: "+g.Reason, schemas.EvidenceOutcome), nil
	}
	v := schemas.Observed(*g.Visual.Score, schemas.EvidenceOutcome)
	v.EvidenceLevel = schemas.EvidenceVerified
	return v, nil
}

// visualJudgeScoreMetric is the vision judge's mean score. It is judged
// evidence: reported beside the verdict, never a trusted measurement.
type visualJudgeScoreMetric struct{}

func newVisualJudgeScoreMetric() *visualJudgeScoreMetric { return &visualJudgeScoreMetric{} }

// GetName implements schemas.Part.
func (m *visualJudgeScoreMetric) GetName() string { return VisualJudgeScore }

// GetVersion implements schemas.Part.
func (m *visualJudgeScoreMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *visualJudgeScoreMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           VisualJudgeScore,
		Version:        version,
		Label:          "Vision judge score",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Mean score (0 to 1) the pinned vision judge gave the captured image against the task's rubric, averaged over runs of tasks with a rubric; judged evidence, unknown when no sample parsed.",
	}
}

// Compute implements schemas.Metric.
func (m *visualJudgeScoreMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !visualRun(rc) || visualRubric(rc) == "" {
		return schemas.NotApplicable(), nil
	}
	g := orEmpty(ev).Grade
	switch {
	case g == nil:
		return schemas.Unknown("run not graded", schemas.EvidenceOutcome), nil
	case g.Visual == nil || g.Visual.Judge == nil:
		return schemas.Unknown("the vision judge was not asked: "+g.Reason, schemas.EvidenceOutcome), nil
	case g.Visual.Judge.Score == nil:
		return schemas.Unknown("no judge sample parsed", schemas.EvidenceOutcome), nil
	}
	v := schemas.Observed(*g.Visual.Judge.Score, schemas.EvidenceOutcome)
	v.EvidenceLevel = schemas.EvidenceInferred
	return v, nil
}

// visualRun reports whether the run's task has output.visual.
func visualRun(rc *schemas.RunContext) bool {
	if rc == nil || rc.Suite == nil {
		return false
	}
	if rc.Task != nil && rc.Task.Output != nil && rc.Task.Output.Visual != nil {
		return true
	}
	return rc.Suite.Output != nil && rc.Suite.Output.Visual != nil
}

func visualRubric(rc *schemas.RunContext) string {
	if rc.Task != nil && rc.Task.Output != nil && rc.Task.Output.Visual != nil {
		return rc.Task.Output.Visual.Rubric
	}
	return rc.Suite.Output.Visual.Rubric
}
