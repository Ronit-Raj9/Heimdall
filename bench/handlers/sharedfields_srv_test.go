package handlers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSharedPageHidesNewFields: the child cause the evaluation answer now
// carries for its members never reaches a shared page (UI fix wave review:
// what a shared page shows waits for the owner).
func TestSharedPageHidesNewFields(t *testing.T) {
	r, root := shareRouter(t)
	ev := schemas.Evaluation{ID: "ev_2", Status: "done", CreatedAt: time.Now(),
		Spec:     schemas.EvaluationSpec{Name: "skip", Mode: "versions", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Version: 1, Model: "gpt-5.4-mini"}}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", Status: "failed", Skipped: "no worker claimed the task"}},
	}
	data, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir), "ev_2.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := shareCall(r, "GET", APIPrefix+"/evaluations/ev_2", nil); !bytes.Contains(res.Body(), []byte(`"cause":"no_worker"`)) {
		t.Fatalf("members should see the cause: %s", res.Body())
	}
	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_2"})
	var sv ShareView
	if err := json.Unmarshal(res.Body(), &sv); err != nil || sv.Token == "" {
		t.Fatalf("create: %d %s", res.StatusCode(), res.Body())
	}
	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil)
	if res.StatusCode() != 200 || bytes.Contains(res.Body(), []byte(`"cause"`)) || !bytes.Contains(res.Body(), []byte("no worker claimed")) {
		t.Fatalf("shared page: %d %s", res.StatusCode(), res.Body())
	}
}

func TestWithoutNewSharedFields(t *testing.T) {
	run := map[string]any{"run": map[string]any{"run_id": "r"}, "task_title": "x", "suite_title": "y", "slot": map[string]any{"task": "t"}}
	got := withoutNewSharedFields("run", run).(map[string]any)
	if _, ok := got["task_title"]; ok {
		t.Fatal("task_title kept")
	}
	if _, ok := got["suite_title"]; ok || got["slot"] == nil {
		t.Fatalf("run part %v", got)
	}
	// Other parts and non-object answers pass through.
	trace := map[string]any{"task_title": "kept"}
	if withoutNewSharedFields("trace", trace).(map[string]any)["task_title"] != "kept" {
		t.Fatal("trace part changed")
	}
	if withoutNewSharedFields("run", nil) != nil {
		t.Fatal("nil part changed")
	}
}
