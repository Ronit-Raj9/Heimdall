package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckScriptPolicyNone covers 7-B-04: BENCH_CI_POLICY=none passes
// `-policy none` to a binary that supports it, so the experiment's frozen
// policy is turned off too, and passes nothing to one that does not.
func TestCheckScriptPolicyNone(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for name, help := range map[string]string{
		"supported": "  -policy string\n    \trelease policy file; none turns policy evaluation off\n",
		"old":       "  -policy string\n    \trelease policy file\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			bin := filepath.Join(dir, "fake-bench")
			script := "#!/usr/bin/env bash\nif [ \"$2\" = -h ]; then printf '%s' '" + help + "' >&2; exit 0; fi\necho \"$*\" > " + argsFile + "\nexit 0\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(dir, "bundle", "e_x")
			if err := os.MkdirAll(bundle, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "check.sh", "recorded")
			cmd.Env = append(os.Environ(), "BENCH_CI_BIN="+bin, "BENCH_CI_BUNDLE="+filepath.Join(dir, "bundle"),
				"BENCH_CI_POLICY=none", "BENCH_CI_OUT="+filepath.Join(dir, "out", "report.md"), "GITHUB_ACTIONS=", "GITHUB_OUTPUT=", "GITHUB_STEP_SUMMARY=")
			_ = cmd.Run() // the fake binary writes no report; only its arguments matter
			got, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("binary not called: %v", err)
			}
			has := strings.Contains(string(got), "-policy none")
			if has != (name == "supported") {
				t.Fatalf("%s binary called with %q", name, got)
			}
		})
	}
}
