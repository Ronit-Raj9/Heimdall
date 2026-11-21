package tenancy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// agents/<id>.yaml@v<N> passes only when the frozen snapshot is inside the
// project, or for v1 of a built-in library agent the project lacks.
func TestCheckExperimentPathsAgentVersions(t *testing.T) {
	root, lib, other := t.TempDir(), t.TempDir(), t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	snaps := filepath.Join(root, ".heimdall", "agent-versions")
	must(os.MkdirAll(filepath.Join(snaps, "a"), 0o755))
	must(os.MkdirAll(filepath.Join(snaps, "esc"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "agents"), 0o755))
	must(os.MkdirAll(filepath.Join(lib, "agents"), 0o755))
	must(os.WriteFile(filepath.Join(root, "agents", "a.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(snaps, "a", "v2.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(lib, "agents", "codex.yaml"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(other, "secret.yaml"), []byte("x"), 0o644))
	must(os.Symlink(filepath.Join(other, "secret.yaml"), filepath.Join(snaps, "esc", "v1.yaml")))

	exp := func(agent string) *schemas.Experiment {
		return &schemas.Experiment{Suite: "s", Base: "agents/a.yaml", Arms: map[string]schemas.ArmSpec{"a": {Agent: agent}}}
	}
	for _, ok := range []string{"agents/a.yaml@v2", "agents/codex.yaml@v1"} {
		if err := CheckExperimentPathsWithLibrary(root, lib, exp(ok)); err != nil {
			t.Fatalf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"agents/a.yaml@v1", "agents/a.yaml@v3", "agents/codex.yaml@v2", "agents/esc.yaml@v1", "agents/none.yaml@v1"} {
		if err := CheckExperimentPathsWithLibrary(root, lib, exp(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if err := CheckExperimentPaths(root, exp("agents/codex.yaml@v1")); err == nil {
		t.Fatal("library version accepted without a library")
	}
}
