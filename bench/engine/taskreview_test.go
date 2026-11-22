package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// reviewBench marks t03 and t04 generated (pending review); t04 is accepted
// in the review sidecar and t05 is rejected there.
func reviewBench(t *testing.T) *bench {
	t.Helper()
	b := newBench(t)
	b.suite.Root = filepath.Join(b.root, "benchmarks", "coding-core")
	gen := []schemas.GeneratedField{{Field: "answer.rubric", Op: "rubric", Model: "openai/gpt-x", PromptSHA: "abc"}}
	for _, id := range []string{"t03", "t04", "t05"} {
		tk := b.suite.TaskByID(id)
		tk.Provenance = &schemas.TaskProvenance{Generated: gen}
		tk.Review = schemas.ReviewPending
	}
	fp := schemas.GeneratedFingerprint(gen)
	f := schemas.TaskReviewFile{Suite: "coding-core", Decisions: map[string]schemas.TaskReviewDecision{
		"t04": {Decision: schemas.ReviewAccepted, By: "ana@example.com", At: time.Now(), Generated: fp},
		"t05": {Decision: schemas.ReviewRejected, By: "ana@example.com", At: time.Now(), Note: "rubric too loose", Generated: fp},
	}}
	data, _ := json.Marshal(f)
	if err := os.MkdirAll(b.suite.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.suite.Root, schemas.TaskReviewFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func plannedTasks(m *schemas.Manifest) []string {
	var out []string
	for _, s := range m.Slots {
		if !slices.Contains(out, s.Task) {
			out = append(out, s.Task)
		}
	}
	slices.Sort(out)
	return out
}

func TestPlanScreensUnreviewedGeneratedTasks(t *testing.T) {
	b := reviewBench(t)
	m, err := NewPlanner(b.reg, b.root, &warnLog{}).Plan(context.Background(), testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}))
	if err != nil {
		t.Fatal(err)
	}
	got := plannedTasks(m)
	if slices.Contains(got, "t03") || slices.Contains(got, "t05") || !slices.Contains(got, "t04") || !slices.Contains(got, "t01") {
		t.Fatalf("tasks: all planned %v: want pending t03 and rejected t05 out, accepted t04 in", got)
	}

	log := &warnLog{}
	m, err = NewPlanner(b.reg, b.root, log).Plan(context.Background(), testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t03"}}, Trials: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if got := plannedTasks(m); !slices.Equal(got, []string{"t01"}) {
		t.Fatalf("explicit list planned %v, want [t01]", got)
	}
	if !strings.Contains(log.joined(), "counts only after a person accepts it") {
		t.Fatalf("no warning about the pending task: %s", log.joined())
	}

	_, err = NewPlanner(b.reg, b.root, &warnLog{}).Plan(context.Background(), testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t05"}}, Trials: 1}))
	if err == nil || !strings.Contains(err.Error(), "t05 (generated, review rejected)") {
		t.Fatalf("only a rejected task: err = %v", err)
	}
}

func TestPlanRegeneratedTaskIsPendingAgain(t *testing.T) {
	b := reviewBench(t)
	// t04 was accepted, then a field was drafted again: the decision no
	// longer matches, so t04 is pending.
	b.suite.TaskByID("t04").Provenance.Generated = append(b.suite.TaskByID("t04").Provenance.Generated,
		schemas.GeneratedField{Field: "controls.exploit", Op: "exploit", Model: "openai/gpt-x", PromptSHA: "def"})
	m, err := NewPlanner(b.reg, b.root, &warnLog{}).Plan(context.Background(), testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plannedTasks(m), "t04") {
		t.Fatalf("regenerated t04 planned: %v", plannedTasks(m))
	}
}

// The readiness check (smoke on the scripted model) of a freshly emitted
// benchmark lists only tasks waiting for review; it must run them, or a
// From issues benchmark can never be admitted (first real hosted run:
// "selects no tasks ... (generated, review pending)"). Rejected stays out.
func TestReadinessPlansPendingGeneratedTasks(t *testing.T) {
	b := reviewBench(t)
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t03", "t05"}}, Trials: 1})
	e.Smoke, e.ScriptedUpstream = true, true
	m, err := NewPlanner(b.reg, b.root, &warnLog{}).Plan(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if got := plannedTasks(m); !slices.Equal(got, []string{"t03"}) {
		t.Fatalf("readiness planned %v, want the pending t03 only", got)
	}
}
