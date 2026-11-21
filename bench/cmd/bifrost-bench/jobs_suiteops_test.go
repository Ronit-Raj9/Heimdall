package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
)

func suiteOpsEnv(t *testing.T, hosted bool) jobEnv {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "benchmarks", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "benchmarks", "core", "suite.yaml"), []byte("id: core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return jobEnv{App: &app{cfg: &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}}, ProjectRoot: root, Hosted: hosted, OrgID: "org_a"}
}

func TestSuiteOpsValidate(t *testing.T) {
	ctx := context.Background()
	local, hosted := suiteOpsEnv(t, false), suiteOpsEnv(t, true)
	for _, tc := range []struct {
		name  string
		env   jobEnv
		v     func(context.Context, jobEnv, json.RawMessage) (any, string, error)
		raw   string
		ok    bool
		match string
	}{
		{"from commit local default repo", local, validateTaskFromCommit, `{"suite":"core","id":"t1","base":"abc","reference":"def"}`, true, ""},
		{"from commit github", hosted, validateTaskFromCommit, `{"suite":"core","id":"t1","base":"abc","reference":"def","github":"https://github.com/acme/app.git"}`, true, ""},
		{"from commit hosted local path", hosted, validateTaskFromCommit, `{"suite":"core","id":"t1","base":"abc","reference":"def","repo":"/etc"}`, false, "GitHub only"},
		{"from commit both sources", local, validateTaskFromCommit, `{"suite":"core","id":"t1","base":"a","reference":"b","repo":".","github":"a/b"}`, false, "not both"},
		{"from commit option base", local, validateTaskFromCommit, `{"suite":"core","id":"t1","base":"--upload-pack=x","reference":"b"}`, false, "not a commit"},
		{"from commit bad github", local, validateTaskFromCommit, `{"suite":"core","id":"t1","base":"a","reference":"b","github":"../x"}`, false, "owner/name"},
		{"from commit missing suite", local, validateTaskFromCommit, `{"suite":"nope","id":"t1","base":"a","reference":"b"}`, false, "nope"},
		{"from commit suite escape", local, validateTaskFromCommit, `{"suite":"..","id":"t1","base":"a","reference":"b"}`, false, ""},
		{"mine hosted real, no oracle agent", hosted, validateTasksMine, `{"suite":"core","github":"acme/app"}`, false, "oracle"},
		{"mine hosted dry run", hosted, validateTasksMine, `{"suite":"core","github":"acme/app","dry_run":true}`, true, ""},
		{"mine local", local, validateTasksMine, `{"suite":"core","limit":3}`, true, ""},
		{"mine limit", local, validateTasksMine, `{"suite":"core","limit":500}`, false, "limit"},
		{"flaky", local, validateQuarantineFlaky, `{"suite":"core"}`, true, ""},
		{"flaky unknown field", local, validateQuarantineFlaky, `{"suite":"core","x":1}`, false, ""},
	} {
		norm, _, err := tc.v(ctx, tc.env, json.RawMessage(tc.raw))
		if tc.ok != (err == nil) {
			t.Errorf("%s: err %v", tc.name, err)
			continue
		}
		if err != nil && tc.match != "" && !strings.Contains(err.Error(), tc.match) {
			t.Errorf("%s: err %q lacks %q", tc.name, err, tc.match)
		}
		if err != nil && !errors.Is(err, jobs.ErrInvalidParams) && !errors.Is(err, jobs.ErrNotFound) {
			t.Errorf("%s: err %v is not a params error", tc.name, err)
		}
		if tc.name == "from commit github" && norm.(taskFromCommitParams).GitHub != "acme/app" {
			t.Errorf("github not normalized: %+v", norm)
		}
	}
}

func TestSuiteOpsMinePreviewLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	env := suiteOpsEnv(t, false)
	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "a.py"), []byte("x = 1\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "init")
	p := tasksMineParams{Suite: "core", Limit: 2, MinTests: 1, DryRun: true, suiteOpsRepo: suiteOpsRepo{Repo: repo}}
	raw, _ := json.Marshal(p)
	_, err := runTasksMineJob(context.Background(), env, &jobs.Job{Params: raw}, discardJobLog{}, nil)
	// suite.yaml names no template: the preview must say what to fix, not crash.
	if err == nil || !strings.Contains(err.Error(), "test globs") {
		t.Fatalf("err %v", err)
	}
}

type discardJobLog struct{}

func (discardJobLog) Printf(string, ...any)       {}
func (discardJobLog) Warnf(string, ...any)        {}
func (discardJobLog) Errorf(string, ...any)       {}
func (discardJobLog) Write(p []byte) (int, error) { return len(p), nil }
