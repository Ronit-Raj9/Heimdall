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

// TestCheckScriptDefaultPolicyKeepsFrozen covers QC2-22-02: an unset
// BENCH_CI_POLICY passes the default policy as -default-policy to a binary
// that has it, never as -policy (which would replace the frozen policy).
func TestCheckScriptDefaultPolicyKeepsFrozen(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for name, help := range map[string]string{
		"supported": "  -default-policy string\n    \trelease policy file evaluated only when the experiment froze none\n  -policy string\n    \trelease policy file; none turns policy evaluation off\n",
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
			def := filepath.Join(dir, "default.yaml")
			if err := os.WriteFile(def, []byte("name: d\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "check.sh", "recorded")
			cmd.Env = append(os.Environ(), "BENCH_CI_BIN="+bin, "BENCH_CI_BUNDLE="+filepath.Join(dir, "bundle"), "BENCH_CI_POLICY=",
				"BENCH_CI_DEFAULT_POLICY="+def, "BENCH_CI_OUT="+filepath.Join(dir, "out", "report.md"), "GITHUB_ACTIONS=", "GITHUB_OUTPUT=", "GITHUB_STEP_SUMMARY=")
			_ = cmd.Run()
			got, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("binary not called: %v", err)
			}
			wantDefault := name == "supported"
			if strings.Contains(string(got), "-default-policy "+def) != wantDefault || strings.Contains(string(got), " -policy ") == wantDefault {
				t.Fatalf("%s binary called with %q", name, got)
			}
		})
	}
}

// TestCheckScriptSidecarUnderDottedFolder covers QC2-22-07: a report path
// without an extension under a dotted folder names the JSON sidecar check
// writes (report.json beside it), not one cut at the folder's dot.
func TestCheckScriptSidecarUnderDottedFolder(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bench")
	// The fake writes the sidecar where the real check does: -out plus .json
	// when the last component has no extension.
	script := "#!/usr/bin/env bash\nif [ \"$2\" = -h ]; then exit 0; fi\nout=\"\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -out ]; then out=$2; fi; shift; done\n" +
		"printf '%s' '{\"verdict\":{\"decision\":\"improved\",\"action\":\"ship\"},\"policy\":{\"decision\":\"allow\"}}' > \"$out.json\"\necho 'verdict: improved, action: ship'\nexit 0\n"
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
	ghOut := filepath.Join(dir, "gh-output")
	out := filepath.Join(dir, "out.with.dot", "report")
	cmd := exec.Command("bash", "check.sh", "recorded")
	cmd.Env = append(os.Environ(), "BENCH_CI_BIN="+bin, "BENCH_CI_BUNDLE="+filepath.Join(dir, "bundle"), "BENCH_CI_POLICY=none",
		"BENCH_CI_OUT="+out, "GITHUB_ACTIONS=", "GITHUB_OUTPUT="+ghOut, "GITHUB_STEP_SUMMARY=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check.sh: %v\n%s", err, b)
	}
	got, err := os.ReadFile(ghOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "report-json="+out+".json\n") || !strings.Contains(string(got), "policy-decision=allow\n") {
		t.Fatalf("outputs:\n%s", got)
	}
}

// TestActionResolvesBundleOnce covers QC2-22-08: the composite action
// resolves the bundle against the gate's project folder once and passes
// that path to both the seal guard and the gate.
func TestActionResolvesBundleOnce(t *testing.T) {
	raw, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "BENCH_CI_BUNDLE: ${{ inputs.bundle }}") {
		t.Fatal("a step still takes the raw bundle input")
	}
	if strings.Count(s, "BENCH_CI_BUNDLE: ${{ steps.paths.outputs.bundle }}") != 2 {
		t.Fatal("the seal guard and the gate do not share the resolved bundle")
	}
	if !strings.Contains(s, `project=$(cd "${BENCH_CI_BENCH_DIR:-$bench}" && pwd)`) {
		t.Fatal("the bundle is not resolved against the gate's project folder")
	}
}
