package suite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A generated task loads (the task schema takes review and
// provenance.generated), is pending, and an accepted decision in the
// sidecar makes it count without touching the lock.
func TestReviewGeneratedTask(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	taskYAML := filepath.Join(packRoot, "tasks", "m01", TaskFile)
	raw, err := os.ReadFile(taskYAML)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("review: pending\nprovenance:\n  created_by: web import\n  generated:\n    - field: answer.rubric\n      op: rubric\n      model: openai/gpt-test\n      prompt_sha: abc\n      cost_usd: 0.002\n      at: \"2026-11-22T10:00:00Z\"\n")...)
	if err := os.WriteFile(taskYAML, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tk := s.TaskByID("m01")
	if !tk.IsGenerated() || len(tk.Provenance.Generated) != 1 {
		t.Fatalf("task %+v", tk.Provenance)
	}
	states, err := ReviewStates(s)
	if err != nil || states["m01"] != schemas.ReviewPending {
		t.Fatalf("states %v %v", states, err)
	}
	if _, err := Review(s, "m01", schemas.ReviewRejected, "ana@example.com", " "); err == nil || !strings.Contains(err.Error(), "needs a note") {
		t.Fatalf("rejection without a note: %v", err)
	}
	if _, err := Review(s, "m01", "maybe", "ana", ""); err == nil {
		t.Fatal("unknown decision accepted")
	}
	before := Stamp(packRoot, true)
	d, err := Review(s, "m01", schemas.ReviewAccepted, "ana@example.com", "checked the proof")
	if err != nil {
		t.Fatal(err)
	}
	if d.By != "ana@example.com" || d.Generated == "" {
		t.Fatalf("decision %+v", d)
	}
	if states, _ := ReviewStates(s); states["m01"] != schemas.ReviewAccepted {
		t.Fatalf("after accept %v", states)
	}
	if Stamp(packRoot, true) == before {
		t.Fatal("the cache stamp does not see the review sidecar")
	}
	// a task a person wrote needs no review
	s.TaskByID("m01").Review = ""
	s.TaskByID("m01").Provenance.Generated = nil
	if _, err := Review(s, "m01", schemas.ReviewAccepted, "ana", ""); err == nil || !strings.Contains(err.Error(), "needs no review") {
		t.Fatalf("hand-written task: %v", err)
	}
}
