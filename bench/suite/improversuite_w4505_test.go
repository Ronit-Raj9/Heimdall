package suite

import (
	"context"
	"path/filepath"
	"testing"
)

// The improver suite of improvement runs loads and lints with no error.
func TestImproverSuiteLints(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "benchmarks"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := newLocalLoader(root).Load(context.Background(), "heimdall-improver")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.ID != "heimdall-improver" || s.Version != "1.1.0" || len(s.Tasks) != 1 || s.Tasks[0].ID != "reflect-edit" {
		t.Fatalf("suite %s@%s, %d tasks", s.ID, s.Version, len(s.Tasks))
	}
	for _, p := range Lint(s) {
		if p.Severity == SeverityError {
			t.Errorf("%s: %s", p.Path, p.Message)
		} else {
			t.Logf("%s %s: %s", p.Severity, p.Path, p.Message)
		}
	}
}
