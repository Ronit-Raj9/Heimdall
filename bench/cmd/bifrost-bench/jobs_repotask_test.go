package main

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRepoTaskURLCheck(t *testing.T) {
	old := repoTaskLookup
	defer func() { repoTaskLookup = old }()
	repoTaskLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "evil.example" {
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		}
		return []net.IP{net.ParseIP("140.82.112.3")}, nil
	}
	ctx := context.Background()
	for raw, ok := range map[string]bool{
		"https://github.com/acme/app.git":        true,
		"http://github.com/acme/app":             false,
		"https://user:tok@github.com/acme/app":   false,
		"https://localhost/acme/app":             false,
		"https://127.0.0.1/x":                    false,
		"https://192.168.1.4/x":                  false,
		"https://evil.example/x":                 false,
		"https://github.com:8443/x":              false,
		"https://github.com/acme/app?u=x":        false,
		"file:///etc":                            false,
		"ext::sh -c id":                          false,
		"https://metadata.google.internal/x.git": false,
	} {
		if _, err := checkRepoURL(ctx, raw); (err == nil) != ok {
			t.Errorf("%s: ok %v, err %v", raw, ok, err)
		}
	}
}

// repoTaskFixture builds the PROOF_REPO.md repository: a buggy
// parse_duration, its fix with two new tests, and a non-fix branch.
func repoTaskFixture(t *testing.T) (repo, base, fix, nonfix string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	buggy := "import re\n\ndef parse_duration(text):\n    total = sum(int(n) * {'h': 3600, 'm': 60, 's': 1}[u] for n, u in re.findall(r'(\\d+)([hms])', text))\n    if total == 0:\n        raise ValueError('bad duration: %r' % text)\n    return total\n"
	tests := "from durations import parse_duration\n\ndef test_hours_minutes():\n    assert parse_duration('1h2m') == 3720\n\ndef test_seconds():\n    assert parse_duration('5s') == 5\n"
	more := tests + "\ndef test_zero_is_valid():\n    assert parse_duration('0s') == 0\n\ndef test_bare_number_is_seconds():\n    assert parse_duration('90') == 90\n"
	git("init", "-q", "-b", "main")
	write(".gitignore", "__pycache__/\n")
	write("durations/__init__.py", buggy)
	write("tests/test_parse.py", tests)
	git("add", "-A")
	git("commit", "-q", "-m", "Add parse_duration")
	base = git("rev-parse", "HEAD")
	write("durations/__init__.py", "import re\n\ndef parse_duration(text):\n    text = text.strip()\n    if text.isdigit():\n        return int(text)\n    if not re.fullmatch(r'(\\d+[hms])+', text):\n        raise ValueError('bad duration: %r' % text)\n    return sum(int(n) * {'h': 3600, 'm': 60, 's': 1}[u] for n, u in re.findall(r'(\\d+)([hms])', text))\n")
	write("tests/test_parse.py", more)
	git("add", "-A")
	git("commit", "-q", "-m", "Fix parse_duration: accept zero and bare numbers as seconds")
	fix = git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "nonfix", base)
	write("durations/__init__.py", buggy+"# TODO\n")
	write("tests/test_parse.py", more)
	git("add", "-A")
	git("commit", "-q", "-m", "Fix parse_duration (comment only)")
	nonfix = git("rev-parse", "HEAD")
	git("checkout", "-q", "main")
	return
}

// pytestAdmit is admission faked at its interface: the reference and the
// empty patch, graded by the task's hidden tests with real pytest.
func pytestAdmit(t *testing.T) func(context.Context, jobEnv, *jobs.Job, string, string, jobs.Logger) (hostedAdmitTask, string, error) {
	return func(ctx context.Context, env jobEnv, _ *jobs.Job, suiteID, id string, _ jobs.Logger) (hostedAdmitTask, string, error) {
		dir := filepath.Join(env.ProjectRoot, "benchmarks", suiteID, "tasks", id)
		run := func(patch bool) bool {
			w := t.TempDir()
			if out, err := exec.Command("cp", "-r", filepath.Join(dir, "repo")+"/.", w).CombinedOutput(); err != nil {
				t.Fatalf("cp: %v %s", err, out)
			}
			if patch {
				cmd := exec.Command("git", "apply", filepath.Join(dir, "solution", "reference.diff"))
				cmd.Dir = w
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("apply: %v %s", err, out)
				}
			}
			if out, err := exec.Command("cp", "-r", filepath.Join(dir, "verifier", "tests", "tests")+"/.", filepath.Join(w, "tests")).CombinedOutput(); err != nil {
				t.Fatalf("cp tests: %v %s", err, out)
			}
			cmd := exec.Command("python3", "-m", "pytest", "-q", "-p", "no:cacheprovider", "tests")
			cmd.Dir = w
			return cmd.Run() == nil
		}
		ref, empty := run(true), run(false)
		res := hostedAdmitTask{Task: id, Admitted: ref && !empty, Checks: []schemas.AdmissionCheck{
			{Name: "reference_1", Want: "pass", Got: map[bool]string{true: "pass", false: "fail"}[ref], Passed: ref},
			{Name: "empty_patch", Want: "fail", Got: map[bool]string{true: "pass", false: "fail"}[empty], Passed: !empty},
		}}
		return res, "", nil
	}
}

func TestRepoTaskBuildFromWebBody(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if exec.Command("python3", "-m", "pytest", "--version").Run() != nil {
		t.Skip("pytest not installed")
	}
	repo, base, fix, nonfix := repoTaskFixture(t)
	env := suiteOpsEnv(t, true)
	if err := os.WriteFile(filepath.Join(env.ProjectRoot, "benchmarks", "core", "suite.yaml"), []byte("# template: python-pytest\nid: core\ngrader: hidden_tests\ntasks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldL, oldC, oldA := repoTaskLookup, suiteOpsGitClone, repoTaskAdmit
	defer func() { repoTaskLookup, suiteOpsGitClone, repoTaskAdmit = oldL, oldC, oldA }()
	repoTaskLookup = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("140.82.112.3")}, nil }
	var sawArgs []string
	suiteOpsGitClone = func(ctx context.Context, args, env []string) error {
		sawArgs = args
		return exec.CommandContext(ctx, "git", "clone", "-q", repo, args[len(args)-1]).Run()
	}
	repoTaskAdmit = pytestAdmit(t)
	ctx := context.Background()
	build := func(ref, id string) *repoTaskResult {
		body := `{"repo_url":"https://github.com/acme/durations.git","base":"` + base + `","reference":"` + ref + `","suite":"core","template":"python-pytest","id":"` + id + `"}`
		norm, _, err := validateRepoTask(ctx, env, json.RawMessage(body))
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		raw, err := json.Marshal(norm)
		if err != nil {
			t.Fatal(err)
		}
		out, err := runRepoTask(ctx, env, &jobs.Job{ID: "j1", Params: raw}, discardJobLog{}, nil)
		if err != nil {
			t.Fatalf("run %s: %v", id, err)
		}
		return out.(*repoTaskResult)
	}
	good := build(fix, "t01")
	if !good.Admitted || len(good.Hidden) != 1 {
		t.Fatalf("fix not admitted: %+v", good)
	}
	if !strings.Contains(strings.Join(sawArgs, " "), "protocol.allow=never") {
		t.Errorf("clone not guarded: %v", sawArgs)
	}
	bad := build(nonfix, "t02")
	if bad.Admitted || !strings.HasPrefix(bad.Reason, "reference_1: want pass, got fail") {
		t.Fatalf("non-fix: %+v", bad)
	}
	for _, body := range []string{
		`{"repo_url":"https://10.0.0.1/a.git","base":"a","reference":"b","suite":"core"}`,
		`{"repo_url":"https://github.com/a/b","base":"a","reference":"a","suite":"core"}`,
		`{"repo_url":"https://github.com/a/b","base":"a","reference":"b","suite":"core","model":"gpt"}`,
		`{"repo_url":"https://github.com/a/b","base":"a","reference":"b","suite":"core","template":"go-test"}`,
	} {
		if _, _, err := validateRepoTask(ctx, env, json.RawMessage(body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestRepoTaskCloneArgsGuarded(t *testing.T) {
	u, _ := url.Parse("https://github.com/acme/app.git")
	got := strings.Join(repoTaskCloneArgs(u, "140.82.112.3", "/tmp/x"), " ")
	for _, want := range []string{"protocol.allow=never", "protocol.https.allow=always", "http.followRedirects=false", "credential.helper=",
		"core.hooksPath=/dev/null", "--no-recurse-submodules", "http.curloptResolve=github.com:443:140.82.112.3", "-- https://github.com/acme/app.git /tmp/x"} {
		if !strings.Contains(got, want) {
			t.Errorf("clone args lack %q: %s", want, got)
		}
	}
}

func TestRepoTaskCloneRefusesLarge(t *testing.T) {
	old := suiteOpsGitClone
	defer func() { suiteOpsGitClone = old }()
	var env []string
	suiteOpsGitClone = func(_ context.Context, args, e []string) error {
		env = e
		dir := args[len(args)-1]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f, err := os.Create(filepath.Join(dir, "big"))
		if err != nil {
			return err
		}
		if err := f.Truncate(repoTaskMaxBytes + 1); err != nil {
			return err
		}
		return f.Close()
	}
	u, _ := url.Parse("https://github.com/acme/app.git")
	_, _, err := repoTaskClone(context.Background(), u, "", discardJobLog{})
	if err == nil || !strings.Contains(err.Error(), "MB limit") {
		t.Fatalf("large clone: %v", err)
	}
	for _, want := range []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}

func TestRepoTaskCandidates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo, base, fix, _ := repoTaskFixture(t)
	env := suiteOpsEnv(t, true)
	if err := os.WriteFile(filepath.Join(env.ProjectRoot, "benchmarks", "core", "suite.yaml"), []byte("# template: python-pytest\nid: core\ngrader: hidden_tests\ntasks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldL, oldC := repoTaskLookup, suiteOpsGitClone
	defer func() { repoTaskLookup, suiteOpsGitClone = oldL, oldC }()
	repoTaskLookup = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("140.82.112.3")}, nil }
	suiteOpsGitClone = func(ctx context.Context, args, _ []string) error {
		return exec.CommandContext(ctx, "git", "clone", "-q", repo, args[len(args)-1]).Run()
	}
	ctx := context.Background()
	norm, _, err := validateRepoCandidates(ctx, env, json.RawMessage(`{"repo_url":"https://github.com/acme/durations.git","suite":"core"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(norm)
	out, err := runRepoCandidates(ctx, env, &jobs.Job{ID: "j2", Params: raw}, discardJobLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cands := out.(map[string]any)["candidates"].([]repoCandidate)
	if len(cands) != 1 || cands[0].Base != base || cands[0].Reference != fix {
		t.Fatalf("candidates %+v, want %s..%s", cands, base, fix)
	}
}
