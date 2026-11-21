package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// agents/<file> the project lacks resolves in the library; the project's
// own file wins, and nothing outside agents/ is layered.
func TestPlannerAgentLibrary(t *testing.T) {
	root, lib := t.TempDir(), t.TempDir()
	for _, f := range []string{filepath.Join(lib, "agents", "lib.yaml"), filepath.Join(lib, "agents", "both.yaml"), filepath.Join(root, "agents", "both.yaml"), filepath.Join(lib, "policies", "p.yaml")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPlanner(nil, root, nil, WithAgentLibrary(lib), WithFilesConfinedTo(root))
	if got := p.abs("agents/lib.yaml"); got != filepath.Join(lib, "agents", "lib.yaml") {
		t.Errorf("library agent: %s", got)
	}
	if got := p.abs("agents/both.yaml"); got != filepath.Join(root, "agents", "both.yaml") {
		t.Errorf("project agent should win: %s", got)
	}
	if got := p.abs("policies/p.yaml"); got != filepath.Join(root, "policies", "p.yaml") {
		t.Errorf("policy layered: %s", got)
	}
	if _, err := p.configFile("agents/lib.yaml"); err != nil {
		t.Errorf("confined library file refused: %v", err)
	}
	if got := NewPlanner(nil, root, nil).abs("agents/lib.yaml"); got != filepath.Join(root, "agents", "lib.yaml") {
		t.Errorf("no library: %s", got)
	}
}
