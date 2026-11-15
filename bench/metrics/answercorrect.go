package metrics

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AnswerCorrect is the name of the answer task success metric.
const AnswerCorrect = "answer_correct"

// AnswerCategoryPrefix prefixes the per-run value of an answer category: a
// task of category "temporal-reasoning" is reported as
// "answer_temporal_reasoning".
const AnswerCategoryPrefix = "answer_"

// gradeModeAnswer is Grade.Mode of a grade reached from an answer.
const gradeModeAnswer = "answer"

// maxCategoryName is the longest category part of a derived name.
const maxCategoryName = 40

var _ schemas.DerivedMetric = (*answerCorrectMetric)(nil)

// answerCorrectMetric scores an answer task run 1 when its answer was graded
// correct and no veto gate fired, exactly as verified_success scores it, and
// through ComputeDerived reports the same value under the task's answer
// category, so results can be read per category (LongMemEval question type,
// LoCoMo category). Both are report-only: the primary endpoint stays
// verified_success.
type answerCorrectMetric struct{}

// GetName implements schemas.Part.
func (m *answerCorrectMetric) GetName() string { return AnswerCorrect }

// GetVersion implements schemas.Part.
func (m *answerCorrectMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *answerCorrectMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           AnswerCorrect,
		Version:        version,
		Label:          "Answer correct",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Share of answer task runs whose answer was graded correct (matched, or judged in a judged suite) with no veto gate fired, over runs with a decided grade; per-category values are reported as answer_<category>.",
		PS2:            false,
	}
}

// Compute implements schemas.Metric. A task that is not an answer task is
// not applicable. Otherwise the value is verified_success's: a fired veto
// scores 0, a missing or undecided grade is unknown.
func (m *answerCorrectMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if !answerRun(rc, ev.Grade) {
		return schemas.NotApplicable(), nil
	}
	return verifiedOutcome(ev), nil
}

// ComputeDerived returns the run's value under AnswerCategoryPrefix plus its
// category (Grade.Category, lower-cased, with every run of characters
// outside [a-z0-9] turned into one underscore, at most 40 characters). A run
// without a category, or whose value is not observed, returns none: readers
// treat a missing name as unknown, never 0. It never fails.
func (m *answerCorrectMetric) ComputeDerived(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (map[string]schemas.Value, error) {
	ev = orEmpty(ev)
	if ev.Grade == nil {
		return nil, nil
	}
	name := categoryName(ev.Grade.Category)
	if name == "" {
		return nil, nil
	}
	v, err := m.Compute(ctx, rc, ev)
	if err != nil || v.State != schemas.ValueObserved {
		return nil, nil
	}
	return map[string]schemas.Value{AnswerCategoryPrefix + name: v}, nil
}

// answerRun reports whether the run is of an answer task: its grade was
// reached from an answer, or the task's output kind is answer.
func answerRun(rc *schemas.RunContext, g *schemas.Grade) bool {
	if g != nil && g.Mode == gradeModeAnswer {
		return true
	}
	return rc != nil && rc.Task != nil && utils.OutputKind(rc.Suite, rc.Task) == schemas.OutputAnswer
}

// categoryName turns a category into the metric name charset.
func categoryName(c string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(c) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			under = false
			b.WriteRune(r)
			continue
		}
		under = true
	}
	s := b.String()
	if len(s) > maxCategoryName {
		s = strings.TrimRight(s[:maxCategoryName], "_")
	}
	return s
}

// newAnswerCorrectMetric returns the answer_correct metric.
func newAnswerCorrectMetric() *answerCorrectMetric {
	return &answerCorrectMetric{}
}
