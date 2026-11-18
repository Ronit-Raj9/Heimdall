package harbor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

// TestImportCompose: a Harbor task with environment/docker-compose.yaml
// imports with its services; Harbor's own variables and log mount are
// understood, a host variable or privileged mode is refused with the place
// in the file, and a compose file Harbor does not read is noted and unused.
func TestImportCompose(t *testing.T) {
	root, res := runImport(t, filepath.Join("testdata", "tb"), "tb-sample", importers.Options{})
	var refused string
	for _, s := range res.Skipped {
		if s.ID == "compose-refused" {
			refused = s.Reason
		}
	}
	for _, want := range []string{"services.main.environment[0]: variable OPENAI_API_KEY is not set", "services.db.privileged: privileged mode"} {
		if !strings.Contains(refused, want) {
			t.Errorf("compose-refused reason %q lacks %q", refused, want)
		}
	}
	s := loadSuite(t, root, "tb-sample")
	task := s.TaskByID("needs-compose")
	if task == nil || task.Compose != "environment/docker-compose.yaml" {
		t.Fatalf("needs-compose: %+v", task)
	}
	if env := task.Environment; env.Dockerfile != "environment/Dockerfile" || env.WorkspacePath != "/app" || env.User != "root" || env.Env["TEST_DIR"] != "/tests" {
		t.Errorf("environment %+v", env)
	}
	if len(task.Services) != 1 || task.Services[0].ID != "db" || !task.Services[0].Compose.Verifier || task.Services[0].Ready == nil {
		t.Fatalf("services %+v", task.Services)
	}
	notes := readFile(t, filepath.Join(root, "benchmarks", "tb-sample", "tasks", "needs-compose", "IMPORT.md"))
	if !strings.Contains(notes, "db run beside it") || !strings.Contains(notes, "Harbor's log folder mount") {
		t.Errorf("IMPORT.md:\n%s", notes)
	}
	yml := s.TaskByID("solo-compose-yml")
	if yml == nil || yml.Compose != "" || len(yml.Services) != 0 {
		t.Fatalf("solo-compose-yml: %+v", yml)
	}
	if n := readFile(t, filepath.Join(root, "benchmarks", "tb-sample", "tasks", "solo-compose-yml", "IMPORT.md")); !strings.Contains(n, "is not read by Harbor") {
		t.Errorf("solo-compose-yml IMPORT.md:\n%s", n)
	}
}
