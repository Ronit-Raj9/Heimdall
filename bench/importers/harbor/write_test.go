package harbor

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPrune(t *testing.T) {
	in := map[string]any{
		"id":       "x",
		"empty":    "",
		"zero":     0,
		"off":      false,
		"notes":    map[string]any{"clean": "", "stale_claims": []string{}},
		"tasks":    []string{},
		"restore":  []string{},
		"required": []string{},
		"keep":     map[string]any{"count": 3, "enabled": true},
	}
	var node yaml.Node
	if err := node.Encode(in); err != nil {
		t.Fatal(err)
	}
	prune(&node)
	out, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, k := range []string{"empty", "zero", "off", "notes", "restore", "clean"} {
		if strings.Contains(got, k+":") {
			t.Errorf("%s kept:\n%s", k, got)
		}
	}
	for _, k := range []string{"id: x", "tasks: []", "required: []", "count: 3", "enabled: true"} {
		if !strings.Contains(got, k) {
			t.Errorf("%s dropped:\n%s", k, got)
		}
	}
}

func TestTarGzDeterministic(t *testing.T) {
	dir := filepath.Join("testdata", "tb", "hello-file", "solution")
	a, err := tarGz(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := tarGz(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) || len(a) == 0 {
		t.Fatalf("two packs differ (%d, %d bytes)", len(a), len(b))
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not installed")
	}
	out := t.TempDir()
	cmd := exec.Command("tar", "-xzf", "-", "-C", out)
	cmd.Stdin = bytes.NewReader(a)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, b)
	}
	info, err := os.Stat(filepath.Join(out, "solve.sh"))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("solve.sh not unpacked executable: %v %v", info, err)
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "skills", "s"), 0o755))
	must(os.WriteFile(filepath.Join(src, "skills", "s", "SKILL.md"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	must(os.Symlink("run.sh", filepath.Join(src, "link.sh")))

	dst := filepath.Join(t.TempDir(), "out")
	must(copyTree(src, dst, map[string]bool{"skills": true}))
	if _, err := os.Stat(filepath.Join(dst, "skills")); !os.IsNotExist(err) {
		t.Errorf("skipped folder copied: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("run.sh: %v %v", info, err)
	}
	if l, err := os.Readlink(filepath.Join(dst, "link.sh")); err != nil || l != "run.sh" {
		t.Errorf("inside link: %q %v", l, err)
	}

	must(os.Symlink("/etc/passwd", filepath.Join(src, "escape")))
	err := copyTree(src, filepath.Join(t.TempDir(), "out2"), nil)
	if _, ok := err.(*skipError); !ok || !strings.Contains(err.Error(), "leaves the task folder") {
		t.Errorf("escaping link: %v", err)
	}
}

func TestSmallHelpers(t *testing.T) {
	if got := taskID("Fix_Git.Repo 2"); got != "fix_git.repo-2" {
		t.Errorf("taskID = %q", got)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote = %q", got)
	}
	if got := commonWorkdir([]*converted{{workdir: "/root"}, {workdir: "/app"}, {workdir: "/root"}}); got != "/root" {
		t.Errorf("commonWorkdir = %q", got)
	}
	if got := seconds(899.2, 5); got != 900 {
		t.Errorf("seconds = %d", got)
	}
	if got := seconds(0, 5); got != 5 {
		t.Errorf("seconds default = %d", got)
	}
	if got := projectName("terminal-bench@2.0"); got != "terminal-bench" {
		t.Errorf("projectName = %q", got)
	}
}

// TestVerifyWrapperRuns runs a generated verify.sh on the host with sh,
// against fake roots: a test.sh that writes a reward must land in the out
// folder, a reward planted before the run must not survive, and when the
// tests mount cannot be created the wrapper runs from its fallback folder
// with the paths in test.sh rewritten.
func TestVerifyWrapperRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	cases := []struct {
		name       string
		readOnly   bool
		rewardFile string // what test.sh writes into the logs folder
		body       string
		want       string // /grader/out/reward.json
	}{
		{"writable", false, "reward.txt", "0.5", "0.5"},
		{"read-only mount", true, "reward.txt", "0.5", "0.5"},
		{"reward json with extra keys", false, "reward.json", `{"reward": 1, "hidden_solution": 0, "read_tests": 0}`, `{"reward": 1}`},
		{"reward json, reward last", false, "reward.json", `{"a": 2,\n "reward": 0.25}`, `{"reward": 0.25}`},
	}
	for _, c := range cases {
		readOnly := c.readOnly
		t.Run(c.name, func(t *testing.T) {
			if readOnly && os.Geteuid() == 0 {
				t.Skip("root ignores folder permissions")
			}
			base := t.TempDir()
			mount := filepath.Join(base, "tests")
			if readOnly {
				ro := filepath.Join(base, "ro")
				if err := os.Mkdir(ro, 0o555); err != nil {
					t.Fatal(err)
				}
				mount = filepath.Join(ro, "tests")
			}
			tk := &upstreamTask{ID: "x", TestsMount: mount, VerifierEnv: map[string]string{"WANT": "ok"}}
			grader, logs, alt := filepath.Join(base, "grader"), filepath.Join(base, "logs"), filepath.Join(base, "alt")
			script := strings.NewReplacer("/grader/", grader+"/", "LOGS=/logs/verifier", "LOGS="+logs, "HERE=/grader/tests", "HERE="+grader+"/tests",
				"ALT=/tmp/harbor-verify", "ALT="+alt).Replace(verifyWrapper("x", tk, base, upstream{Benchmark: "b@1"}))
			for _, d := range []string{"tests/upstream", "tests/harbor", "out"} {
				if err := os.MkdirAll(filepath.Join(grader, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(logs, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(logs, "reward.txt"), []byte("1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// The fake test script names the mount and the logs folder the way
			// upstream scripts do, so the read-only case checks the rewrite.
			test := "#!/bin/sh\nLOGDIR=" + logs + "\n[ ! -e \"$LOGDIR/reward.txt\" ] || exit 9\n" +
				"[ -f " + mount + "/data.txt ] && [ \"$WANT\" = ok ] && printf '%s' '" + c.body + "' > " + logs + "/" + c.rewardFile + "\n"
			if readOnly {
				// Upstream scripts write /logs/verifier, which the wrapper rewrites.
				test = strings.ReplaceAll(test, logs, "/logs/verifier")
			}
			if err := os.WriteFile(filepath.Join(grader, "tests/harbor/test.sh"), []byte(test), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(grader, "tests/upstream/data.txt"), []byte("d"), 0o644); err != nil {
				t.Fatal(err)
			}
			if readOnly {
				// /logs/verifier is not writable on the host either: make the
				// wrapper's normal logs folder unusable too.
				script = strings.Replace(script, "LOGS="+logs, "LOGS="+filepath.Join(base, "ro", "logs"), 1)
			}
			out, err := exec.Command("sh", "-c", script).CombinedOutput()
			if err != nil {
				t.Fatalf("verify.sh: %v\n%s", err, out)
			}
			got, err := os.ReadFile(filepath.Join(grader, "out", "reward.json"))
			if err != nil || strings.TrimSpace(string(got)) != c.want {
				t.Errorf("reward = %q, %v\n%s", got, err, out)
			}
			if readOnly && !strings.Contains(string(out), "running from "+alt) {
				t.Errorf("no fallback notice:\n%s", out)
			}
		})
	}
}
