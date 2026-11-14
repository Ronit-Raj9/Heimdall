package suite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestValidateTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tests := []struct {
		name     string
		edit     func(t *testing.T, pack string, s *schemas.Suite)
		wantPath string
		wantMsg  string
		wantSev  string
	}{
		{"clean", nil, "", "", ""},
		{"reference does not apply", func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/repo/calc/core.py"), "def add", "def add_numbers")
		}, "tasks/m01/solution/patch.diff", "does not apply cleanly", SeverityError},
		{"git metadata in repo", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.MkdirAll(filepath.Join(pack, "tasks/m01/repo/.git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/repo", "holds git metadata (.git)", SeverityError},
		{"secret", func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.WriteFile(filepath.Join(pack, "tasks/m01/repo/keys.txt"), []byte("one\nghp_0123456789abcdefghijABCDEFGHIJ012345\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "tasks/m01/repo/keys.txt", "line 2: possible GitHub token (ghp_****)", SeverityError},
		{"not listed", func(t *testing.T, _ string, s *schemas.Suite) {
			s.TaskIDs = []string{"other"}
		}, "suite.yaml", `"m01" is not listed`, SeverityError},
		{"tests from a reference commit", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Provenance = &schemas.TaskProvenance{ReferenceCommit: "0123456789abcdef0123"}
		}, "tasks/m01/verifier/tests", "come from reference commit 0123456789ab", SeverityWarning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			s := loadMini(t, benchRoot)
			if tc.edit != nil {
				tc.edit(t, packRoot, s)
			}
			problems := ValidateTask(context.Background(), s, s.Tasks[0])
			if tc.wantMsg == "" {
				for _, p := range problems {
					if p.Severity == SeverityError {
						t.Fatalf("clean task has errors: %+v", problems)
					}
				}
				return
			}
			for _, p := range problems {
				if p.Path == tc.wantPath && strings.Contains(p.Message, tc.wantMsg) {
					if p.Severity != tc.wantSev {
						t.Errorf("severity = %s, want %s", p.Severity, tc.wantSev)
					}
					if strings.Contains(p.Message, "0123456789abcdefghij") {
						t.Errorf("secret value printed: %s", p.Message)
					}
					return
				}
			}
			t.Fatalf("no problem %s: %q in %+v", tc.wantPath, tc.wantMsg, problems)
		})
	}
}

func TestApplyCheckEmptyPatch(t *testing.T) {
	patch := filepath.Join(t.TempDir(), "empty.diff")
	if err := os.WriteFile(patch, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyCheck(context.Background(), t.TempDir(), patch); err != nil {
		t.Fatalf("empty patch: %v", err)
	}
}
