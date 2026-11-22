package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/validate"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
)

type ibFakeJobs struct {
	created []jobs.NewJob
	result  issuebench.BuildResult
	gets    int
}

func (f *ibFakeJobs) Kinds() []string { return nil }
func (f *ibFakeJobs) Create(_ context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.created = append(f.created, n)
	return &jobs.Job{ID: "j1", OrgID: n.OrgID, Kind: n.Kind, Status: jobs.StatusQueued}, nil
}
func (f *ibFakeJobs) Get(_ context.Context, org, id string) (*jobs.Job, error) {
	f.gets++
	b, _ := json.Marshal(f.result)
	return &jobs.Job{ID: id, OrgID: org, Status: jobs.StatusSucceeded, Result: b}, nil
}
func (f *ibFakeJobs) List(context.Context, string, jobs.Filter) (*jobs.Page, error) { return nil, nil }
func (f *ibFakeJobs) Logs(context.Context, string, string, int64, int) (*jobs.LogPage, error) {
	return nil, nil
}
func (f *ibFakeJobs) Cancel(context.Context, string, string) (*jobs.Job, error) { return nil, nil }

// ibFakeCommands answers every command with ok, except names listed in fail.
func ibFakeCommands(t *testing.T, fail ...string) *[]string {
	t.Helper()
	var calls []string
	old := ibCommand
	ibCommand = func(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ")
		calls = append(calls, line)
		for _, f := range fail {
			if strings.HasPrefix(line, f) {
				return []byte("not found"), errors.New("exit 1")
			}
		}
		return nil, nil
	}
	t.Cleanup(func() { ibCommand = old })
	return &calls
}

func ibEnv(t *testing.T, j handlers.JobsAPI) handlers.OrgEnv {
	return handlers.OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Services: handlers.Services{OrgID: "org1", Jobs: j}}
}

func TestIssueBenchStagesWired(t *testing.T) {
	if handlers.IssueBenchStages == nil {
		t.Fatal("IssueBenchStages is not set")
	}
	st := handlers.IssueBenchStages(ibEnv(t, nil))
	if st.Scanner == nil || st.Builder == nil || st.Validator == nil || st.Emitter == nil {
		t.Fatalf("a stage is missing: %+v", st)
	}
}

func TestIssueBenchStagesPlainStops(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "")
	ctx := context.Background()
	logf := func(string, ...any) {}
	c := issuebench.Candidate{Repo: "o/r", Issue: 7, BaseSHA: "abc", PRMerged: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}

	st := issuebenchStagesFor(ibEnv(t, nil))
	if _, _, err := st.Scanner.Scan(ctx, "o/r", pipeline.ScanOptions{}); err == nil || !strings.Contains(err.Error(), "GitHub is not connected") {
		t.Fatalf("scan without a connection: %v", err)
	}
	if _, err := st.Builder.Build(ctx, c, pipeline.BuildOptions{}, logf); err == nil || !strings.Contains(err.Error(), "no job runner") {
		t.Fatalf("build without jobs: %v", err)
	}
	st = issuebenchStagesFor(ibEnv(t, &ibFakeJobs{}))
	if _, err := st.Builder.Build(ctx, c, pipeline.BuildOptions{}, logf); err == nil || !strings.Contains(err.Error(), "BENCH_ISSUEBENCH_PROVIDER") {
		t.Fatalf("build without a model: %v", err)
	}
	ibFakeCommands(t, "docker version")
	if _, err := st.Validator.Validate(ctx, c, issuebench.EnvSpec{}, logf); err == nil || !strings.Contains(err.Error(), "docker is not available") {
		t.Fatalf("validate without docker: %v", err)
	}
	if _, err := st.Emitter.Emit(ctx, "o/r", []issuebench.Validation{{Candidate: c}}); err == nil || !strings.Contains(err.Error(), "environment is not on this server") {
		t.Fatalf("emit without an env: %v", err)
	}
}

func TestIssueBenchBuilderQueuesJobAndKeepsEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "openai")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "gpt-x")
	calls := ibFakeCommands(t)
	old := ibPoll
	ibPoll = time.Millisecond
	t.Cleanup(func() { ibPoll = old })
	fj := &ibFakeJobs{result: issuebench.BuildResult{Stop: issuebench.StopOK, Env: &issuebench.EnvSpec{Dockerfile: "FROM python:3.12"}}}
	env := ibEnv(t, fj)
	st := issuebenchStagesFor(env)
	c := issuebench.Candidate{Repo: "o/r", Issue: 7, BaseSHA: "abc", PRMerged: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	res, err := st.Builder.Build(context.Background(), c, pipeline.BuildOptions{BudgetUSD: 9}, func(string, ...any) {})
	if err != nil || res.Stop != issuebench.StopOK {
		t.Fatalf("build: %+v %v", res, err)
	}
	if len(fj.created) != 1 || fj.created[0].Kind != kindIssuebenchBuild || fj.created[0].OrgID != "org1" {
		t.Fatalf("job: %+v", fj.created)
	}
	var p issuebenchBuildParams
	_ = json.Unmarshal(fj.created[0].Params, &p)
	if !p.Consent || p.MaxUSD != 9 || p.Version != "2026-03" || p.Provider != "openai" || !strings.HasPrefix(p.RepoDir, env.Cfg.Root) {
		t.Fatalf("params: %+v", p)
	}
	for _, l := range *calls {
		if strings.Contains(l, "clone") && strings.Contains(l, "https://") && !strings.Contains(l, "http.extraheader") {
			t.Fatalf("token not passed per command: %s", l)
		}
	}
	s := &ibStages{root: env.Cfg.Root}
	if e, ok := s.loadEnv("o/r", "2026-03"); !ok || e.Dockerfile == "" {
		t.Fatal("built env not kept for emit")
	}
}

func TestIssueBenchRunnerScript(t *testing.T) {
	c := &issuebench.Candidate{TestPatch: "diff --git a/t b/t\n", FixPatch: "diff --git a/f b/f\n"}
	base := ibScript(c, validate.Run{State: validate.StateBase, Command: "pytest -rA"})
	if strings.Contains(base, "git apply") || !strings.HasSuffix(base, "pytest -rA\n") {
		t.Fatalf("base script: %q", base)
	}
	gold := ibScript(c, validate.Run{State: validate.StateGold, Command: "pytest -rA"})
	if strings.Count(gold, "git apply") != 2 || strings.Index(gold, "tests.patch") > strings.Index(gold, "fix.patch") {
		t.Fatalf("gold script: %q", gold)
	}
	old := ibCommand
	ibCommand = func(context.Context, []byte, string, ...string) ([]byte, error) {
		return []byte(ibPatchFailed + " fix"), errors.New("exit 97")
	}
	t.Cleanup(func() { ibCommand = old })
	if _, err := (ibRunner{image: "img"}).Run(context.Background(), nil, c, validate.Run{State: validate.StateGold, Command: "x"}); !errors.Is(err, validate.ErrPatchApply) {
		t.Fatalf("patch failure: %v", err)
	}
}
