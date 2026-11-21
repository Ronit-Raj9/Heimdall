package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAdmittedTasksSplitAndQuarantine checks an evaluation's task pick: a
// public evaluation never picks a held-out task, a held-out one picks only
// held-out tasks, and a quarantined task is left out alone with its reason
// instead of skipping the benchmark.
func TestAdmittedTasksSplitAndQuarantine(t *testing.T) {
	root := t.TempDir()
	st := &schemas.Suite{ID: "s", Root: root, TaskIDs: []string{"t1", "t2", "t3", "t4", "h1", "h2"}}
	for _, id := range st.TaskIDs {
		task := &schemas.Task{ID: id}
		if id[0] == 'h' {
			task.Split = schemas.SplitHeldOut
		}
		st.Tasks = append(st.Tasks, task)
	}
	data, err := json.Marshal(schemas.SuiteHealthFile{Suite: "s", Quarantined: map[string]schemas.TaskQuarantine{
		"t2": {Reason: "verifier flaky"}, "h2": {},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, schemas.SuiteHealthFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	pub := admittedTasks(st, schemas.SplitPublic)
	if !slices.Equal(pub.IDs, []string{"t1", "t3", "t4"}) {
		t.Fatalf("public pick %v", pub.IDs)
	}
	if len(pub.Skipped) != 1 || pub.Skipped[0].Task != "t2" || pub.Skipped[0].Reason != "quarantined: verifier flaky" {
		t.Fatalf("public skipped %+v", pub.Skipped)
	}
	held := admittedTasks(st, schemas.SplitHeldOut)
	if !slices.Equal(held.IDs, []string{"h1"}) || len(held.Skipped) != 1 || held.Skipped[0].Reason != "quarantined" {
		t.Fatalf("held-out pick %+v", held)
	}
	if got := evalAdmittedTasks(st); !slices.Equal(got, pub.IDs) {
		t.Fatalf("estimate picks %v, evaluation %v", got, pub.IDs)
	}
}

// TestEvaluationSplitNormalize checks split is public unless held_out is
// asked for, and nothing else is accepted.
func TestEvaluationSplitNormalize(t *testing.T) {
	h := &evaluationsHandler{}
	for in, want := range map[string]string{"": "", "public": "", " held_out ": schemas.SplitHeldOut} {
		spec := schemas.EvaluationSpec{Split: in}
		if err := h.normalizeSplit(&spec); err != nil || spec.Split != want {
			t.Fatalf("split %q: %q %v", in, spec.Split, err)
		}
	}
	if err := h.normalizeSplit(&schemas.EvaluationSpec{Split: "secret"}); err == nil {
		t.Fatal("bad split accepted")
	}
}
