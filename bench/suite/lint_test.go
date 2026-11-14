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
		wantSev  string // default error
	}{
		{"instruction mentions hidden test", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/instruction.md"), "too.", "too. The hidden tests check this.")
		}, "tasks/m01/instruction.md", `"hidden test"`, ""},
		{"instruction mentions CLAUDE.md", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/instruction.md"), "too.", "too. See claude.md.")
		}, "tasks/m01/instruction.md", `"claude.md"`, ""},
		{"requirements miss a test", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/verifier/requirements.md"), "| `test_add_mixed` |", "| `test_add_mixed_signs` |")
		}, "tasks/m01/verifier/requirements.md", "does not mention hidden test test_add_mixed (test_hidden.py)", ""},
		{"requirements missing", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.Remove(filepath.Join(pack, "tasks/m01/verifier/requirements.md")); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/verifier/requirements.md", "missing", SeverityWarning},
		{"required id unknown", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Verifier.Required = []string{"test_hidden.py::test_add_zero"}
		}, "tasks/m01/task.yaml", "no function test_add_zero in test_hidden.py", ""},
		{"no stale notes", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Notes.Stale = ""
		}, "tasks/m01/task.yaml", "notes.stale: missing", ""},
		{"no stale notes and no stale-notes probe", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Notes = schemas.TaskNotes{}
			s.ProbeIDs = nil
			s.Probes = nil
			s.Tasks[0].Controls.Mutants = nil
		}, "tasks/m01/task.yaml", "controls.mutants: empty", SeverityWarning},
		{"draft instruction", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/instruction.md"), "too.", "too.\n"+EditMeMarker)
		}, "tasks/m01/instruction.md", "EDIT ME", ""},
		{"verifier stub not finished", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.WriteFile(filepath.Join(pack, "tasks/m01/verifier/tests/test.sh"), []byte("#!/bin/sh\n# EDIT ME: write the check\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/verifier/tests/test.sh", "EDIT ME", ""},
		{"hidden test visible in repo", func(t *testing.T, pack string, _ *schemas.Suite) {
			b, err := os.ReadFile(filepath.Join(pack, "tasks/m01/verifier/tests/test_hidden.py"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pack, "tasks/m01/repo/tests/test_copy.py"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/verifier/tests/test_hidden.py", "visible in the repo as tests/test_copy.py", ""},
		{"go required id unknown", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Verifier.Required = []string{"example.com/calc::TestAddZero"}
		}, "tasks/m01/task.yaml", "no func TestAddZero", ""},
		{"empty mutants", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Controls.Mutants = nil
		}, "tasks/m01/task.yaml", "controls.mutants: empty", SeverityWarning},
		{"unknown layer", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Probes[0].Layer = "session"
		}, "probes/stale-notes.yaml", `layer: "session"`, ""},
		{"duplicate task", func(t *testing.T, _ string, s *schemas.Suite) {
			s.TaskIDs = append(s.TaskIDs, "m01")
			s.Tasks = append(s.Tasks, s.Tasks[0])
		}, "suite.yaml", `id "m01" listed more than once`, ""},
		{"no visible tests", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.RemoveAll(filepath.Join(pack, "tasks/m01/repo/tests")); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/repo", "no visible tests", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			s := loadMini(t, benchRoot)
			tc.edit(t, packRoot, s)
			problems := Lint(s)
			for _, p := range problems {
				if p.Path == tc.wantPath && strings.Contains(p.Message, tc.wantMsg) {
					want := tc.wantSev
					if want == "" {
						want = SeverityError
					}
					if p.Severity != want {
						t.Errorf("severity = %s, want %s", p.Severity, want)
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

func TestLintNoStaleNotesWithoutProbe(t *testing.T) {
	benchRoot, _ := copyPack(t)
	s := loadMini(t, benchRoot)
	s.Tasks[0].Notes = schemas.TaskNotes{}
	s.ProbeIDs, s.Probes = nil, nil
	for _, p := range Lint(s) {
		if strings.Contains(p.Message, "notes.stale") {
			t.Fatalf("stale notes reported without the stale-notes probe: %+v", p)
		}
	}
}

func TestEditMeLine(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{EditMeMarker + "\n# Title\n", 1},
		{"# Title\n\n  " + EditMeMarker + "\n", 3},
		{"#!/bin/sh\n# EDIT ME: replace the check\n", 2},
		{"# Title\nPlease edit me later.\n", 0},
	}
	for _, tc := range tests {
		if got := editMeLine([]byte(tc.text)); got != tc.want {
			t.Errorf("editMeLine(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	tests := map[string]bool{
		"tests/test_a.py": true, "pkg/a_test.py": true, "pkg/a.py": false, "tests/conftest.py": false,
		"calc/calc_test.go": true, "calc/calc.go": false,
		"src/a.test.ts": true, "test/a.js": true, "lib/test-util.mjs": true, "src/a.js": false, "test/fixture.json": false,
	}
	for p, want := range tests {
		if got := isTestFile(p); got != want {
			t.Errorf("isTestFile(%s) = %v, want %v", p, got, want)
		}
	}
}
