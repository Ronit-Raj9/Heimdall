package tenancy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The built-in library stands in for agents/<file> the project lacks, read
// only: a project link (even one that escapes) still decides, a policy or a
// path outside agents/ never falls back, and a library-only suite id passes.
func TestCheckExperimentPathsWithLibrary(t *testing.T) {
	root, lib, other := t.TempDir(), t.TempDir(), t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "agents"), 0o755))
	must(os.MkdirAll(filepath.Join(lib, "agents"), 0o755))
	must(os.MkdirAll(filepath.Join(lib, "policies"), 0o755))
	must(os.WriteFile(filepath.Join(root, "agents", "a.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(lib, "agents", "codex-tb2.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(lib, "agents", "link.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(lib, "policies", "p.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(other, "secret.yaml"), []byte("x"), 0o644))
	must(os.Symlink(filepath.Join(other, "secret.yaml"), filepath.Join(root, "agents", "link.yaml")))

	exp := func(agent string) *schemas.Experiment {
		return &schemas.Experiment{Suite: "hd-harbor-tb2", Base: "agents/a.yaml",
			Arms: map[string]schemas.ArmSpec{"a": {Agent: agent}, "b": {}}}
	}
	if err := CheckExperimentPathsWithLibrary(root, lib, exp("agents/codex-tb2.yaml")); err != nil {
		t.Fatalf("library agent refused: %v", err)
	}
	if err := CheckExperimentPaths(root, exp("agents/codex-tb2.yaml")); err == nil {
		t.Fatal("library agent accepted without a library")
	}
	if err := CheckExperimentPathsWithLibrary(root, lib, exp("agents/link.yaml")); err == nil {
		t.Fatal("escaping project link accepted because the library has the name")
	}
	e := exp("agents/a.yaml")
	e.Policy = "policies/p.yaml"
	if err := CheckExperimentPathsWithLibrary(root, lib, e); err == nil {
		t.Fatal("library policy accepted")
	}
	if err := CheckExperimentPathsWithLibrary(root, lib, exp("../"+filepath.Base(lib)+"/agents/codex-tb2.yaml")); err == nil {
		t.Fatal("dotdot into the library accepted")
	}
}
