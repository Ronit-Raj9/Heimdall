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

// TestActionSkipsGoCacheWithPrebuiltBinary: when the caller passes a
// prebuilt binary (heimdall ci install), it has already run setup-go with
// the module cache, so the action's own setup-go must not restore that
// cache again. On GitHub the second restore over the same GOMODCACHE made
// tar log about 10,000 "Cannot open: File exists" error annotations.
func TestActionSkipsGoCacheWithPrebuiltBinary(t *testing.T) {
	raw, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "uses: actions/setup-go@")
	if i < 0 {
		t.Fatal("action.yml has no setup-go step")
	}
	step := s[i:]
	if j := strings.Index(step, "\n    - name:"); j >= 0 {
		step = step[:j]
	}
	if !strings.Contains(step, "cache: ${{ inputs.bin == '' }}") {
		t.Fatalf("setup-go restores the module cache even with a prebuilt binary:\n%s", step)
	}
}

// TestCheckScriptNoDefaults: check.sh has no default mode and no default
// bundle, so a gate added without inputs fails with a usage error (64)
// instead of re-checking Heimdall's own test bundle and passing.
func TestCheckScriptNoDefaults(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-bench")
	if err := os.WriteFile(bin, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no mode":   {nil, "set the mode"},
		"no bundle": {[]string{"recorded"}, "recorded mode needs BENCH_CI_BUNDLE"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", append([]string{"check.sh"}, tc.args...)...)
			cmd.Env = append(os.Environ(), "BENCH_CI_BIN="+bin, "BENCH_CI_MODE=", "BENCH_CI_BUNDLE=",
				"BENCH_CI_OUT="+filepath.Join(dir, "out", "report.md"), "GITHUB_ACTIONS=", "GITHUB_OUTPUT=", "GITHUB_STEP_SUMMARY=")
			out, err := cmd.CombinedOutput()
			ee, ok := err.(*exec.ExitError)
			if !ok || ee.ExitCode() != 64 {
				t.Fatalf("exit %v, want 64\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestActionHasNoDemoDefaults: the composite action requires mode and has
// no default bundle (D4: the old default re-checked testdata/recorded).
func TestActionHasNoDemoDefaults(t *testing.T) {
	raw, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "default: testdata/recorded") {
		t.Fatal("action.yml still defaults to the recorded test bundle")
	}
	i := strings.Index(s, "  mode:\n")
	j := strings.Index(s, "  experiment:\n")
	if i < 0 || j < i || !strings.Contains(s[i:j], "required: true") || strings.Contains(s[i:j], "default:") {
		t.Fatal("the mode input is not required or still has a default")
	}
	if !strings.Contains(s, "recorded mode needs the bundle input") {
		t.Fatal("the action does not refuse recorded mode without a bundle")
	}
}
