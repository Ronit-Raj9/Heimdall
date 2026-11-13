package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLintClean(t *testing.T) {
	benchRoot, _ := copyPack(t)
	if problems := Lint(loadMini(t, benchRoot)); len(problems) != 0 {
		t.Fatalf("valid pack has lint problems: %+v", problems)
	}
}

func TestLint(t *testing.T) {
	tests := []struct {
		name     string
		edit     func(t *testing.T, pack string, s *schemas.Suite)
		wantPath string
		wantMsg  string
	}{
		{"instruction mentions hidden test", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/instruction.md"), "too.", "too. The hidden tests check this.")
		}, "tasks/m01/instruction.md", `"hidden test"`},
		{"instruction mentions CLAUDE.md", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/instruction.md"), "too.", "too. See claude.md.")
		}, "tasks/m01/instruction.md", `"claude.md"`},
		{"requirements miss a test", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/verifier/requirements.md"), "| `test_add_mixed` |", "| `test_add_mixed_signs` |")
		}, "tasks/m01/verifier/requirements.md", "does not mention hidden test test_add_mixed (test_hidden.py)"},
		{"requirements missing", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.Remove(filepath.Join(pack, "tasks/m01/verifier/requirements.md")); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/verifier/requirements.md", "missing"},
		{"required id unknown", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Verifier.Required = []string{"test_hidden.py::test_add_zero"}
		}, "tasks/m01/task.yaml", "no function test_add_zero in test_hidden.py"},
		{"no stale notes", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Notes.Stale = ""
		}, "tasks/m01/task.yaml", "notes.stale: missing"},
		{"empty mutants", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Controls.Mutants = nil
		}, "tasks/m01/task.yaml", "controls.mutants: empty"},
		{"unknown layer", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Probes[0].Layer = "session"
		}, "probes/stale-notes.yaml", `layer: "session"`},
		{"duplicate task", func(t *testing.T, _ string, s *schemas.Suite) {
			s.TaskIDs = append(s.TaskIDs, "m01")
			s.Tasks = append(s.Tasks, s.Tasks[0])
		}, "suite.yaml", `id "m01" listed more than once`},
		{"no visible tests", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.RemoveAll(filepath.Join(pack, "tasks/m01/repo/tests")); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/repo", "no visible tests"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			s := loadMini(t, benchRoot)
			tc.edit(t, packRoot, s)
			problems := Lint(s)
			for _, p := range problems {
				if p.Path == tc.wantPath && strings.Contains(p.Message, tc.wantMsg) {
					if p.Severity != SeverityError {
						t.Errorf("severity = %s, want error", p.Severity)
					}
					return
				}
			}
			t.Fatalf("no problem %s: %q in %+v", tc.wantPath, tc.wantMsg, problems)
		})
	}
}

func TestHiddenTests(t *testing.T) {
	dir := t.TempDir()
	src := "def test_a():\n    pass\n\nclass TestX:\n    def test_b(self):\n        pass\n\nasync def test_c():\n    pass\n\ndef helper_test_d():\n    pass\n"
	if err := os.WriteFile(filepath.Join(dir, "test_x.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conftest.py"), []byte("def test_not_collected():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	funcs, err := hiddenTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sortedFuncNames(funcs), ",")
	if got != "test_a,test_b,test_c" {
		t.Fatalf("hidden tests = %s", got)
	}
}
