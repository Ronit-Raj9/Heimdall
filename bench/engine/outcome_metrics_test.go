package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Audit 1-C-01: a graded pass whose cleanup failed is sealed infra_error with
// Y unknown; verified_success must not stay an observed 1, in memory or in
// the sealed metrics.json.
func TestCleanupFailureMakesVerifiedSuccessUnknown(t *testing.T) {
	b, m, slot := pipelineFixture(t, "stale-notes")
	b.w.fail["destroy"] = true
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil || res == nil {
		t.Fatalf("res %v err %v", res, err)
	}
	if res.Run.Outcome != schemas.OutcomeInfraError || res.Run.Y.IsObserved() {
		t.Fatalf("outcome %s y %+v", res.Run.Outcome, res.Run.Y)
	}
	if v, ok := res.Metrics["verified_success"]; !ok || v.IsObserved() {
		t.Fatalf("verified_success in memory = %+v (present %v)", v, ok)
	}
	raw, err := os.ReadFile(filepath.Join(res.Dir, "metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sealed map[string]schemas.Value
	if err := json.Unmarshal(raw, &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed["verified_success"].IsObserved() {
		t.Fatalf("sealed verified_success = %+v", sealed["verified_success"])
	}
}

// reportOnlyMetric is a fake metric built only from agent reports.
type reportOnlyMetric struct{ fakeMetric }

func (m reportOnlyMetric) Descriptor() schemas.MetricDescriptor {
	d := m.fakeMetric.Descriptor()
	d.ReportOnly = true
	return d
}

// Audit 1-C-02: a report-only metric (agent-reported steps) must not be a
// primary or guard endpoint.
func TestPlanRejectsReportOnlyEndpoint(t *testing.T) {
	for _, guard := range []bool{false, true} {
		b := newBench(t)
		b.reg.Metrics = append(b.reg.Metrics, reportOnlyMetric{fakeMetric{part{"reported_steps"}, b.w}})
		e := testExperiment()
		e.Metrics = append(e.Metrics, "reported_steps")
		if guard {
			e.Guards = append(e.Guards, schemas.GuardSpec{Condition: e.Primary.Condition, Rule: "no_regression", Metric: "reported_steps"})
		} else {
			e.Primary.Metric = "reported_steps"
		}
		_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
		if err == nil || !strings.Contains(err.Error(), "report-only") {
			t.Fatalf("guard=%v err = %v, want report-only rejection", guard, err)
		}
	}
}

// Audit 1-B-02: a sequential rule must stop on the primary condition only,
// and an unknown trials condition is refused instead of dropped.
func TestPlanSequentialConditionMustBePrimary(t *testing.T) {
	tests := []struct {
		name  string
		rule  string
		conds []string
		want  string
	}{
		{"sequential on a probe condition", schemas.AdaptiveSequential, []string{"stale-notes"}, "only the primary condition"},
		{"sequential on primary and probe", schemas.AdaptiveSequential, []string{"clean", "stale-notes"}, "only the primary condition"},
		{"unknown condition", schemas.AdaptiveTask, []string{"clean", "nope"}, `"nope" is not a condition`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			e := testExperiment(
				schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1},
				schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
			e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: tt.rule, Looks: 2, Conditions: tt.conds}
			if tt.rule == schemas.AdaptiveTask {
				e.Trials.Looks, e.Trials.Min, e.Trials.Max = 0, 1, 1
				e.Conditions[0].Trials, e.Conditions[1].Trials = 2, 2
				e.Trials.Max = 2
			}
			_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want %q", err, tt.want)
			}
		})
	}
	b := newBench(t)
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1})
	e.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 2, Conditions: []string{"clean"}}
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err != nil {
		t.Fatalf("primary-only sequential refused: %v", err)
	}
}
