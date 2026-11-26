package policybench

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/taskcat"
	"github.com/maximhq/bifrost/bench/taskguard"
)

// TestPolicyAcceptWritesGuard: an accepted policy task has guard.json with
// builder policy, a clear-by-person instruction and the policy category.
func TestPolicyAcceptWritesGuard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "suite.yaml"), []byte("id: s\ntasks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Doc{ID: "refunds", Name: "refunds.md", Drafts: []Draft{{Task: "p1", Clause: "2.1", Status: StatusPending}}}
	if err := os.MkdirAll(DraftDir(root, d.ID, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(DraftDir(root, d.ID, "p1"), "task.yaml"), []byte("id: p1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Accept(root, d, []string{"p1"}, "ana", time.Now(), nil, func(old, next []byte, note string) error {
		return os.WriteFile(filepath.Join(root, "suite.yaml"), next, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	r, err := taskguard.Read(filepath.Join(root, "tasks", "p1"))
	if err != nil || r == nil || r.Builder != taskguard.BuilderPolicy || r.Clarity == nil || r.Clarity.Origin != taskguard.OriginPerson || r.Source == "" {
		t.Fatalf("guard record = %+v %v", r, err)
	}
	cats, err := taskcat.Read(root)
	if err != nil || cats["p1"].Category != taskcat.Policy {
		t.Fatalf("categories = %+v %v", cats, err)
	}
}
