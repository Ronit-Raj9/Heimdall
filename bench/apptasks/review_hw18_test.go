package apptasks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/dataset"
)

// Reviewing a written task records a dataset version: accept as "Review
// accept", reject (the task leaves the benchmark) as "Task deleted" (fix
// wave 3, N268).
func TestReviewRecordsDatasetVersion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "benchmarks", "shop")
	for _, id := range []string{"a", "b"} {
		td := filepath.Join(dir, "tasks", id)
		if err := os.MkdirAll(td, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(td, "task.yaml"), []byte("id: "+id+"\ntitle: Task "+id+"\nprovenance:\n  source: openapi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(td, "instruction.md"), []byte("Do "+id+".\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte("id: shop\ntasks: [a, b]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Draft{Suite: "shop", Written: []Written{{ID: "a", Review: ReviewPending}, {ID: "b", Review: ReviewPending}}}
	if err := Review(dir, d, "a", ReviewAccepted, "alice"); err != nil {
		t.Fatal(err)
	}
	vs, err := dataset.Open(root).Versions("shop")
	if err != nil || len(vs) != 1 || vs[0].Source != "Review accept" || len(vs[0].Added) != 2 {
		t.Fatalf("after accept: %+v %v", vs, err)
	}
	if err := Review(dir, d, "b", ReviewRejected, "alice"); err != nil {
		t.Fatal(err)
	}
	vs, _ = dataset.Open(root).Versions("shop")
	if len(vs) != 2 || vs[1].Source != "Task deleted" || len(vs[1].Removed) != 1 || vs[1].Removed[0] != "b" {
		t.Fatalf("after reject: %+v", vs)
	}
}
