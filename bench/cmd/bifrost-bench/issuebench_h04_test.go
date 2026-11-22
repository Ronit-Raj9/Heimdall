package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/emit"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/validate"
	"github.com/maximhq/bifrost/bench/jobs"
)

// Lane H04 tests: model checks through a job, the form's model choice,
// emit's image layers and an absolute org root.

type ibCheckFakeModel struct {
	replies []string
	err     error
	calls   int
}

func (m *ibCheckFakeModel) Complete(context.Context, []validate.Message) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	if len(m.replies) == 0 {
		return "", errors.New("no reply")
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}
func (m *ibCheckFakeModel) Spent() (float64, int) { return 0.02 * float64(m.calls), m.calls }

func ibCheckParams(mut func(*issuebenchCheckParams)) json.RawMessage {
	p := issuebenchCheckParams{Candidate: issuebench.Candidate{Repo: "o/r", Issue: 7, IssueTitle: "t", IssueBody: "b", FixPatch: "diff --git a/x.py b/x.py\n"},
		Provider: "openai", Model: "m1", MaxUSD: 1, Consent: true}
	if mut != nil {
		mut(&p)
	}
	b, _ := json.Marshal(p)
	return b
}

func TestIssuebenchCheckJob(t *testing.T) {
	ctx := context.Background()
	for name, mut := range map[string]func(*issuebenchCheckParams){
		"consent": func(p *issuebenchCheckParams) { p.Consent = false },
		"model":   func(p *issuebenchCheckParams) { p.Model = "" },
		"max_usd": func(p *issuebenchCheckParams) { p.MaxUSD = 0 },
		"fix":     func(p *issuebenchCheckParams) { p.Candidate.FixPatch = "" },
	} {
		if _, _, err := validateIssuebenchCheck(ctx, jobEnv{}, ibCheckParams(mut)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s: want invalid params, got %v", name, err)
		}
	}
	if _, target, err := validateIssuebenchCheck(ctx, jobEnv{}, ibCheckParams(nil)); err != nil || target != "o/r#7" {
		t.Fatalf("valid: %v %q", err, target)
	}

	old := issuebenchCheckModelFor
	t.Cleanup(func() { issuebenchCheckModelFor = old })
	var fake *ibCheckFakeModel
	closed := 0
	var gotCap float64
	issuebenchCheckModelFor = func(_ context.Context, _ jobEnv, p issuebenchCheckParams, _ string) (issuebenchCheckModel, func(), error) {
		gotCap = p.MaxUSD
		return fake, func() { closed++ }, nil
	}
	// A leak the model quotes stops the task.
	fake = &ibCheckFakeModel{replies: []string{`{"leak":true,"leak_evidence":"the issue says the fix","clarity":1}`}}
	res, err := runIssuebenchCheck(ctx, jobEnv{}, &jobs.Job{ID: "j_1", Params: ibCheckParams(nil)}, &testJobLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := res.(issuebenchCheckResult)
	if !r.Checks.Leak || r.Stop != validate.StopLeak || r.Calls != 1 || r.CostUSD <= 0 || closed != 1 || gotCap != 1 {
		t.Fatalf("leak: %+v closed=%d cap=%v", r, closed, gotCap)
	}
	// The budget running out is a stop, not an error.
	fake = &ibCheckFakeModel{err: validate.ErrBudget}
	res, err = runIssuebenchCheck(ctx, jobEnv{}, &jobs.Job{ID: "j_2", Params: ibCheckParams(nil)}, &testJobLog{}, nil)
	if err != nil || res.(issuebenchCheckResult).Stop != validate.StopModelBudget || closed != 2 {
		t.Fatalf("budget: %+v %v", res, err)
	}
}

// ibRawJobs answers every job with result.
type ibRawJobs struct {
	ibFakeJobs
	raw any
}

func (f *ibRawJobs) Get(_ context.Context, org, id string) (*jobs.Job, error) {
	b, _ := json.Marshal(f.raw)
	return &jobs.Job{ID: id, OrgID: org, Status: jobs.StatusSucceeded, Result: b}, nil
}

func TestIssueBenchModelChecksStage(t *testing.T) {
	old := ibPoll
	ibPoll = time.Millisecond
	t.Cleanup(func() { ibPoll = old })
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "")
	t.Setenv("BENCH_ISSUEBENCH_CHECK_MAX_USD", "0.5")
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, f) }
	fj := &ibRawJobs{raw: issuebenchCheckResult{Checks: issuebench.LLMChecks{Clear: true, WrongPatches: []string{"p1", "p2"}}, Calls: 3, CostUSD: 0.1}}
	s := &ibStages{orgID: "org1", jobs: fj}
	v := &issuebench.Validation{Candidate: issuebench.Candidate{Repo: "o/r", Issue: 7, FixPatch: "x"}}

	// No model picked and none set: skipped, a person reads the issue.
	if err := s.modelChecks(context.Background(), v, logf); err != nil || len(fj.created) != 0 || v.Checks.Clear {
		t.Fatalf("no model: %v %+v", err, fj.created)
	}
	ctx := pipeline.WithModel(context.Background(), pipeline.ModelChoice{Provider: "anthropic", Model: "claude-x", KeyID: "k9"})
	if err := s.modelChecks(ctx, v, logf); err != nil {
		t.Fatal(err)
	}
	if len(fj.created) != 1 || fj.created[0].Kind != kindIssuebenchCheck || fj.created[0].OrgID != "org1" {
		t.Fatalf("job: %+v", fj.created)
	}
	var p issuebenchCheckParams
	_ = json.Unmarshal(fj.created[0].Params, &p)
	if p.Provider != "anthropic" || p.Model != "claude-x" || p.KeyID != "k9" || p.MaxUSD != 0.5 || !p.Consent || p.Candidate.Issue != 7 {
		t.Fatalf("params: %+v", p)
	}
	if !v.Checks.Clear || len(v.Checks.WrongPatches) != 2 || v.Stop != "" {
		t.Fatalf("checks not on the validation: %+v", v)
	}
}

func TestIssueBenchBuilderUsesFormModel(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "openai")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "gpt-x")
	ibFakeCommands(t)
	fj := &ibFakeJobs{result: issuebench.BuildResult{Stop: issuebench.StopOK}}
	st := issuebenchStagesFor(ibEnv(t, fj))
	c := issuebench.Candidate{Repo: "o/r", Issue: 7, BaseSHA: "abc", PRMerged: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	opts := pipeline.BuildOptions{ModelChoice: pipeline.ModelChoice{Provider: "anthropic", Model: "claude-x", KeyID: "k2"}}
	if _, err := st.Builder.Build(context.Background(), c, opts, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	var p issuebenchBuildParams
	_ = json.Unmarshal(fj.created[0].Params, &p)
	if p.Provider != "anthropic" || p.Model != "claude-x" || p.KeyID != "k2" {
		t.Fatalf("form model not used: %+v", p)
	}
}

func TestIssueBenchStagesAbsoluteRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	env := ibEnv(t, nil)
	env.Cfg.Root = "proj"
	st := issuebenchStagesFor(env)
	root := st.Builder.(ibBuilder).s.root
	if !filepath.IsAbs(root) {
		t.Fatalf("root %q is not absolute", root)
	}
	// The repo_dir the builder sends passes the job's own check on the same root.
	repoDir := st.Builder.(ibBuilder).s.dir("src", "o__r@2026-03")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := issuebenchBuildParams{Repo: "o/r", Version: "2026-03", RepoDir: repoDir, Candidates: []issuebench.Candidate{{Repo: "o/r"}},
		Provider: "openai", Model: "m", MaxUSD: 1, Consent: true}
	if _, err := p.check("proj"); err != nil {
		t.Fatalf("relative project root: %v", err)
	}
}

func TestIssueBenchBuildLayers(t *testing.T) {
	dir := t.TempDir()
	l := emit.Layers{BaseRef: "heimdall-issues/base:py", EnvRef: "heimdall-issues/env-o-r:1", TaskRef: "heimdall-issues/task-o-r-1:abc",
		BaseDockerfile: "FROM python:3.12\n", EnvDockerfile: "FROM heimdall-issues/base:py\n", TaskDockerfile: "FROM heimdall-issues/env-o-r:1\n"}
	b, _ := json.Marshal(l)
	for name, body := range map[string]string{"layers.json": string(b), "Dockerfile.base": l.BaseDockerfile, "Dockerfile.env": l.EnvDockerfile, "Dockerfile": l.TaskDockerfile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The base exists; env and task are built from the tar of the context.
	calls := ibFakeCommands(t, "docker image inspect heimdall-issues/env", "docker image inspect heimdall-issues/task")
	if err := (ibDockerLayers{}).BuildLayers(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	var builds []string
	for _, c := range *calls {
		if strings.HasPrefix(c, "sh -c") || strings.HasPrefix(c, "docker build") {
			builds = append(builds, c)
		}
	}
	if len(builds) != 2 || !strings.HasSuffix(builds[0], dir+" "+l.EnvRef+" Dockerfile.env") || !strings.HasSuffix(builds[1], dir+" "+l.TaskRef+" Dockerfile") {
		t.Fatalf("builds: %q", builds)
	}
	if !strings.Contains(builds[0], "tar -C") || !strings.Contains(builds[0], "-ch") {
		t.Fatalf("context not sent through tar -h: %s", builds[0])
	}
	ibFakeCommands(t, "docker version")
	if err := (ibDockerLayers{}).BuildLayers(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "docker is not available") {
		t.Fatalf("no docker: %v", err)
	}
}
