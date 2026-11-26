package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestTaskMetadataGetsCategory(t *testing.T) {
	root := t.TempDir()
	side := `{"a":{"category":"bug_fix","sure":true},"b":{"category":"refactor","sure":false},"c":{"category":"docs","sure":true}}`
	if err := os.WriteFile(filepath.Join(root, categoriesFile), []byte(side), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &schemas.Suite{Root: root, Tasks: []*schemas.Task{
		{ID: "a", Metadata: map[string]string{"split": "x"}}, {ID: "b"}, {ID: "c"}, {ID: "d"},
	}}
	got := taskMetadata(suite, []condPlan{{tasks: []string{"a", "b", "d"}}})
	if got["a"]["category"] != "bug_fix" || got["a"]["split"] != "x" {
		t.Errorf("a = %v", got["a"])
	}
	if got["b"]["category"] != "refactor (unsure)" {
		t.Errorf("b = %v", got["b"])
	}
	if _, ok := got["c"]; ok {
		t.Errorf("unused task c in metadata: %v", got)
	}
	if _, ok := got["d"]; ok {
		t.Errorf("task d without label or metadata in metadata: %v", got)
	}
	if err := os.WriteFile(filepath.Join(root, categoriesFile), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := taskMetadata(suite, []condPlan{{tasks: []string{"b"}}}); got != nil {
		t.Errorf("bad sidecar gave %v", got)
	}
}
