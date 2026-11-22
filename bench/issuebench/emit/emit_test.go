package emit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

const fixPatch = `diff --git a/calc.py b/calc.py
--- a/calc.py
+++ b/calc.py
@@ -1,2 +1,2 @@
 def add(a, b):
-    return a - b
+    return a + b
`

const testPatch = `diff --git a/tests/test_calc.py b/tests/test_calc.py
new file mode 100644
--- /dev/null
+++ b/tests/test_calc.py
@@ -0,0 +1,4 @@
+from calc import add
+
+def test_add():
+    assert add(1, 2) == 3
`

// repoWithHistory makes a checkout with a secret in a later commit, a tag
// and a remote, then resets to the first commit (the base).
func repoWithHistory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), scrubEnv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "dev")
	must(t, os.WriteFile(filepath.Join(dir, "calc.py"), []byte("def add(a, b):\n    return a - b\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	must(t, os.WriteFile(filepath.Join(dir, "calc.py"), []byte("def add(a, b):\n    return a + b  # THE_FIX\n"), 0o644))
	run("commit", "-q", "-am", "fix")
	run("tag", "v1")
	run("remote", "add", "origin", "https://example.invalid/r.git")
	run("reset", "-q", "--hard", "HEAD~1")
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestScrubGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	ctx := context.Background()
	dir := repoWithHistory(t)
	sha, err := ScrubGit(ctx, dir)
	must(t, err)
	if len(sha) != 40 {
		t.Fatalf("sha %q", sha)
	}
	out, err := git(ctx, dir, "log", "--all", "-p")
	must(t, err)
	if strings.Contains(out, "THE_FIX") {
		t.Fatal("fix still readable from history")
	}
	// Same tree, same commit.
	again, err := ScrubGit(ctx, dir)
	must(t, err)
	if again != sha {
		t.Fatalf("scrub not deterministic: %s vs %s", sha, again)
	}
}

func TestHeldOutByDate(t *testing.T) {
	var cs []issuebench.Candidate
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 10; i++ {
		cs = append(cs, issuebench.Candidate{Repo: "o/r", PR: i, IssueCreated: day.AddDate(0, 0, i)})
	}
	h := HeldOut(cs, DefaultHeldOut)
	if len(h) != 2 || !h["o__r-10"] || !h["o__r-9"] {
		t.Fatalf("newest two expected, got %v", h)
	}
	h = HeldOut(cs, HeldOutPlan{After: day.AddDate(0, 0, 7)})
	if len(h) != 3 || !h["o__r-8"] {
		t.Fatalf("after cutoff: %v", h)
	}
	if len(HeldOut(cs[:3], DefaultHeldOut)) != 0 {
		t.Fatal("small repo should hold none out")
	}
}

func TestLayersShareEnv(t *testing.T) {
	env := issuebench.EnvSpec{Repo: "o/r", Version: "1.2", BaseImage: "python:3.11-slim", Commands: []string{"pip install -e ."}}
	a, err := LayersOf(env, "o__r-1", strings.Repeat("a", 40))
	must(t, err)
	b, err := LayersOf(env, "o__r-2", strings.Repeat("b", 40))
	must(t, err)
	if a.BaseRef != b.BaseRef || a.EnvRef != b.EnvRef || a.TaskRef == b.TaskRef {
		t.Fatalf("layers: %+v %+v", a, b)
	}
	if !strings.HasPrefix(a.EnvDockerfile, "FROM "+a.BaseRef) || !strings.HasPrefix(a.TaskDockerfile, "FROM "+a.EnvRef) {
		t.Fatal("layers not chained")
	}
	if _, err := LayersOf(issuebench.EnvSpec{Repo: "o/r"}, "x", "y"); err == nil {
		t.Fatal("no base image accepted")
	}
}

type fakeQueue struct{ kinds []string }

func (f *fakeQueue) Queue(_ context.Context, kind string, params any) (string, error) {
	b, _ := json.Marshal(params)
	f.kinds = append(f.kinds, kind+" "+string(b))
	return "job-" + kind, nil
}

func TestEmit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	ctx := context.Background()
	root := t.TempDir()
	env := issuebench.EnvSpec{Repo: "Acme/Calc", Version: "1.0", Language: "python", BaseImage: "python:3.11-slim", Commands: []string{"pip install pytest"}, TestCmd: "pytest -rA"}
	mk := func(pr int, day int, leak bool) Input {
		return Input{
			Env:     env,
			RepoDir: repoWithHistory(t),
			Validation: issuebench.Validation{
				Candidate: issuebench.Candidate{
					Repo: "Acme/Calc", PR: pr, Issue: 100 + pr, IssueTitle: "add is wrong",
					IssueBody: "add(1, 2) returns -1", IssueCreated: time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC),
					TestPatch: testPatch, FixPatch: fixPatch, Language: "python",
				},
				FailToPass: []string{"tests/test_calc.py::test_add"},
				Checks:     issuebench.LLMChecks{Leak: leak, Clear: true, WrongPatches: []string{strings.Replace(fixPatch, "a + b", "a * b", 1)}},
			},
		}
	}
	var in []Input
	for i := 1; i <= 5; i++ {
		in = append(in, mk(i, i, false))
	}
	in = append(in, mk(9, 9, true))
	q := &fakeQueue{}
	rep, err := Emit(ctx, in, Options{BenchRoot: root, WorkDir: filepath.Join(root, "work"), Readiness: q})
	must(t, err)
	if len(rep.Suites) != 1 {
		t.Fatalf("suites %d", len(rep.Suites))
	}
	s := rep.Suites[0]
	if s.Suite != "issues-acme-calc" || len(s.Tasks) != 5 || len(s.Skipped) != 1 {
		t.Fatalf("suite %+v", s)
	}
	if len(s.HeldOut) != 1 || s.HeldOut[0] != s.Tasks[len(s.Tasks)-1] {
		t.Fatalf("held out %v of %v", s.HeldOut, s.Tasks)
	}
	if len(q.kinds) != 3 || !strings.HasPrefix(q.kinds[2], "suite.admit") || !strings.Contains(q.kinds[2], `"suite":"issues-acme-calc"`) {
		t.Fatalf("readiness %v", q.kinds)
	}
	for _, tid := range s.Tasks {
		_, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), s.Suite, tid)
		must(t, err)
		if task.Review != schemas.ReviewPending {
			t.Fatalf("%s review %q", tid, task.Review)
		}
		if len(task.Controls.Mutants) != 1 {
			t.Fatalf("%s mutants %v", tid, task.Controls.Mutants)
		}
		if task.Environment == nil || !strings.HasPrefix(task.Environment.Image, ImageNamespace+"/task-") {
			t.Fatalf("%s image %+v", tid, task.Environment)
		}
		if task.Provenance.ImportedFrom != "" {
			t.Fatalf("%s imported_from %q", tid, task.Provenance.ImportedFrom)
		}
		base, err := os.ReadFile(filepath.Join(root, "benchmarks", s.Suite, "tasks", tid, "verifier/tests/base_commit"))
		must(t, err)
		bd := s.BuildDirs[tid]
		head, err := git(ctx, filepath.Join(bd, "repo"), "rev-parse", "HEAD")
		must(t, err)
		if strings.TrimSpace(string(base)) != head {
			t.Fatalf("base_commit %s, scrubbed head %s", base, head)
		}
		for _, f := range []string{"Dockerfile.base", "Dockerfile.env", "Dockerfile"} {
			if _, err := os.Stat(filepath.Join(bd, f)); err != nil {
				t.Fatal(err)
			}
		}
	}
	instr, err := os.ReadFile(filepath.Join(root, "benchmarks", s.Suite, "tasks", s.Tasks[0], "instruction.md"))
	must(t, err)
	if !strings.Contains(string(instr), "add(1, 2) returns -1") {
		t.Fatalf("instruction %q", instr)
	}
}
