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

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
)

// ibWorkJobs answers each job by kind: work jobs with the next queued
// WorkResult, check jobs with a clear check result, others with "{}".
type ibWorkJobs struct {
	created []jobs.NewJob
	work    []issuebench.WorkResult
	results map[string][]byte
}

func (f *ibWorkJobs) Kinds() []string { return nil }
func (f *ibWorkJobs) Create(_ context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.created = append(f.created, n)
	id := "j" + string(rune('0'+len(f.created)))
	if f.results == nil {
		f.results = map[string][]byte{}
	}
	switch n.Kind {
	case issuebench.WorkJobKind:
		b, _ := json.Marshal(f.work[0])
		f.work = f.work[1:]
		f.results[id] = b
	case kindIssuebenchCheck:
		b, _ := json.Marshal(issuebenchCheckResult{Checks: issuebench.LLMChecks{Clear: true}, Calls: 1})
		f.results[id] = b
	default:
		f.results[id] = []byte("{}")
	}
	return &jobs.Job{ID: id, OrgID: n.OrgID, Kind: n.Kind, Status: jobs.StatusQueued}, nil
}
func (f *ibWorkJobs) Get(_ context.Context, org, id string) (*jobs.Job, error) {
	return &jobs.Job{ID: id, OrgID: org, Status: jobs.StatusSucceeded, Result: f.results[id]}, nil
}
func (f *ibWorkJobs) List(context.Context, string, jobs.Filter) (*jobs.Page, error) { return nil, nil }
func (f *ibWorkJobs) Logs(context.Context, string, string, int64, int) (*jobs.LogPage, error) {
	return nil, nil
}
func (f *ibWorkJobs) Cancel(context.Context, string, string) (*jobs.Job, error) { return nil, nil }

func (f *ibWorkJobs) kinds() []string {
	var out []string
	for _, n := range f.created {
		out = append(out, n.Kind)
	}
	return out
}

func ibHostedEnv(t *testing.T, j handlers.JobsAPI) handlers.OrgEnv {
	return handlers.OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Services: handlers.Services{OrgID: "org1", Jobs: j, Hosted: true}}
}

// ibNoDocker fails every docker command (the hosted server has none) and
// records every command; git commands succeed, and a clone writes a file.
func ibNoDocker(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := ibCommand
	ibCommand = func(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name != "git" {
			return []byte("docker is not here"), errors.New("exit 1")
		}
		if len(args) > 2 && args[0] == "clone" && args[2] == "--no-hardlinks" {
			dir := args[len(args)-1]
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(dir, "calc.py"), []byte("def add(a, b):\n    return a - b\n"), 0o644)
		}
		return nil, nil
	}
	oldPoll := ibPoll
	ibPoll = time.Millisecond
	t.Cleanup(func() { ibCommand, ibPoll = old, oldPoll })
	return &calls
}

func ibHostedCandidate() issuebench.Candidate {
	return issuebench.Candidate{
		Repo: "acme/calc", Issue: 7, PR: 8, BaseSHA: strings.Repeat("a", 40), PRMerged: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		IssueTitle: "add is wrong", IssueBody: "add(1, 2) returns -1", Language: "python",
		TestPatch: "diff --git a/t.py b/t.py\n", FixPatch: "diff --git a/calc.py b/calc.py\n",
	}
}

func TestIssuebenchHostedBuildQueuesWorkJob(t *testing.T) {
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "openai")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "gpt-x")
	t.Setenv(issuebench.EgressAllowEnv, " Registry.NPMjs.org ,pypi.org,, ")
	calls := ibNoDocker(t)
	c := ibHostedCandidate()
	env := &issuebench.EnvSpec{BaseImage: "python:3.12", Dockerfile: "FROM python:3.12\n", TestCmd: "pytest -rA"}
	val := issuebench.Validation{Candidate: c, FailToPass: []string{"t.py::test_add"}}
	fj := &ibWorkJobs{work: []issuebench.WorkResult{{Mode: issuebench.WorkModeBuild, Build: &issuebench.BuildResult{Env: env, Stop: issuebench.StopOK}, Validation: &val, CostUSD: 0.42}}}
	oe := ibHostedEnv(t, fj)
	st := issuebenchStagesFor(oe)
	var noted []string
	ctx := issuebench.WithJobNote(context.Background(), func(stage, id string) { noted = append(noted, stage+":"+id) })

	res, err := st.Builder.Build(ctx, c, pipeline.BuildOptions{BudgetUSD: 9, ModelChoice: pipeline.ModelChoice{Provider: "anthropic", Model: "m1", KeyID: "k1"}}, func(string, ...any) {})
	if err != nil || res.Stop != issuebench.StopOK || res.CostUSD != 0.42 {
		t.Fatalf("build: %+v %v", res, err)
	}
	if len(fj.created) != 1 || fj.created[0].Kind != issuebench.WorkJobKind || fj.created[0].OrgID != "org1" {
		t.Fatalf("jobs: %+v", fj.created)
	}
	var p issuebench.WorkParams
	if err := json.Unmarshal(fj.created[0].Params, &p); err != nil {
		t.Fatal(err)
	}
	r := p.Request
	if !p.Consent || p.KeyID != "k1" || r.Mode != issuebench.WorkModeBuild || r.OrgID != "org1" || r.Repo != c.Repo || r.Version != "2026-03" ||
		r.Candidate.Issue != 7 || r.Provider != "anthropic" || r.Model != "m1" || r.MaxUSD != 9 || r.Env != nil {
		t.Fatalf("params: %+v", p)
	}
	if strings.Join(r.EgressHosts, ",") != strings.Join(append(append([]string{}, issuebench.DefaultEgressHosts...), "registry.npmjs.org"), ",") {
		t.Fatalf("egress hosts: %v", r.EgressHosts)
	}
	r.JobID = "job1"
	if err := r.Check(); err != nil {
		t.Fatalf("request does not check: %v", err)
	}
	if len(noted) != 1 || noted[0] != issuebench.StageBuild+":j1" {
		t.Fatalf("noted %v", noted)
	}
	if len(*calls) != 0 {
		t.Fatalf("hosted build ran commands on the server: %v", *calls)
	}
	s := &ibStages{root: oe.Cfg.Root}
	if e, ok := s.loadEnv(c.Repo, "2026-03"); !ok || e.Repo != c.Repo || e.Version != "2026-03" {
		t.Fatalf("env not kept: %+v", e)
	}
	if _, err := os.Stat(s.validationPath(c)); err != nil {
		t.Fatalf("validation not cached: %v", err)
	}

	// A second build of the same version sends the prior env as a hint.
	fj.work = []issuebench.WorkResult{{Stop: "the builder ran out of budget"}}
	if _, err := st.Builder.Build(ctx, c, pipeline.BuildOptions{}, func(string, ...any) {}); err == nil || err.Error() != "the builder ran out of budget" {
		t.Fatalf("stop: %v", err)
	}
	_ = json.Unmarshal(fj.created[1].Params, &p)
	if p.Request.Env == nil || p.Request.Env.Dockerfile != env.Dockerfile || p.Request.Provider != "openai" {
		t.Fatalf("second build params: %+v", p.Request)
	}
}

func TestIssuebenchHostedValidateUsesCacheThenJob(t *testing.T) {
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "openai")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "gpt-x")
	t.Setenv(issuebench.EgressAllowEnv, "")
	calls := ibNoDocker(t)
	c := ibHostedCandidate()
	env := issuebench.EnvSpec{Repo: c.Repo, Version: "2026-03", BaseImage: "python:3.12", Dockerfile: "FROM python:3.12\n"}
	cached := issuebench.Validation{Candidate: c, FailToPass: []string{"from-build"}}
	fresh := issuebench.Validation{Candidate: c, FailToPass: []string{"from-validate"}}
	fj := &ibWorkJobs{work: []issuebench.WorkResult{{Mode: issuebench.WorkModeValidate, Validation: &fresh}}}
	oe := ibHostedEnv(t, fj)
	st := issuebenchStagesFor(oe)
	s := &ibStages{root: oe.Cfg.Root}
	s.saveValidation(c, env.Dockerfile, cached)
	var noted []string
	ctx := issuebench.WithJobNote(context.Background(), func(stage, id string) { noted = append(noted, stage) })
	logf := func(string, ...any) {}

	v, err := st.Validator.Validate(ctx, c, env, logf)
	if err != nil || len(v.FailToPass) != 1 || v.FailToPass[0] != "from-build" || !v.Checks.Clear {
		t.Fatalf("cached: %+v %v", v, err)
	}
	if strings.Join(fj.kinds(), ",") != kindIssuebenchCheck {
		t.Fatalf("cached run queued %v", fj.kinds())
	}
	if _, err := os.Stat(s.validationPath(c)); !os.IsNotExist(err) {
		t.Fatalf("cache not removed: %v", err)
	}

	// The Dockerfile changed since the build: the cache does not count.
	s.saveValidation(c, env.Dockerfile, cached)
	env.Dockerfile = "FROM python:3.12\nRUN pip install pytest\n"
	v, err = st.Validator.Validate(ctx, c, env, logf)
	if err != nil || v.FailToPass[0] != "from-validate" {
		t.Fatalf("validate job: %+v %v", v, err)
	}
	if strings.Join(fj.kinds(), ",") != kindIssuebenchCheck+","+issuebench.WorkJobKind+","+kindIssuebenchCheck {
		t.Fatalf("jobs %v", fj.kinds())
	}
	var p issuebench.WorkParams
	_ = json.Unmarshal(fj.created[1].Params, &p)
	if p.Request.Mode != issuebench.WorkModeValidate || p.Request.Env == nil || p.Request.Env.Dockerfile != env.Dockerfile || !p.Consent {
		t.Fatalf("validate params: %+v", p)
	}
	p.Request.JobID = "job1"
	if err := p.Request.Check(); err != nil {
		t.Fatalf("validate request does not check: %v", err)
	}
	if strings.Join(noted, ",") != "checks,validate,checks" {
		t.Fatalf("noted %v", noted)
	}
	if len(*calls) != 0 {
		t.Fatalf("hosted validate ran commands on the server: %v", *calls)
	}
}

func TestIssuebenchHostedEmitSelfContained(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_fake_token_for_emit_test")
	calls := ibNoDocker(t)
	old := ibLayers
	ibLayers = ibLayersFunc(func(context.Context, string) error { return errors.New("no layer may be built on the hosted server") })
	t.Cleanup(func() { ibLayers = old })
	c := ibHostedCandidate()
	c.TestPatch = "diff --git a/tests/test_calc.py b/tests/test_calc.py\nnew file mode 100644\n--- /dev/null\n+++ b/tests/test_calc.py\n@@ -0,0 +1,4 @@\n+from calc import add\n+\n+def test_add():\n+    assert add(1, 2) == 3\n"
	c.FixPatch = "diff --git a/calc.py b/calc.py\n--- a/calc.py\n+++ b/calc.py\n@@ -1,2 +1,2 @@\n def add(a, b):\n-    return a - b\n+    return a + b\n"
	c.IssueCreated = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	fj := &ibWorkJobs{}
	oe := ibHostedEnv(t, fj)
	s := &ibStages{root: oe.Cfg.Root}
	s.saveEnv(issuebench.EnvSpec{Repo: c.Repo, Version: "2026-03", Language: "python", BaseImage: "python:3.11-slim", Commands: []string{"pip install pytest"}, Dockerfile: "FROM python:3.11-slim\n", TestCmd: "pytest -rA"})
	st := issuebenchStagesFor(oe)
	id, err := st.Emitter.Emit(context.Background(), c.Repo, []issuebench.Validation{{Candidate: c, FailToPass: []string{"tests/test_calc.py::test_add"}, Checks: issuebench.LLMChecks{Clear: true}}})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.Join(fj.kinds(), ",") != strings.Join(pipeline.AdmitKinds, ",") {
		t.Fatalf("queued %v", fj.kinds())
	}
	for _, l := range *calls {
		if strings.HasPrefix(l, "docker") || strings.HasPrefix(l, "sh") {
			t.Fatalf("hosted emit ran %s", l)
		}
	}
	tasks, _ := filepath.Glob(filepath.Join(oe.Cfg.Root, "benchmarks", id, "tasks", "*", "environment", "repo.tar.gz"))
	if len(tasks) != 1 {
		t.Fatalf("self-contained tasks: %v", tasks)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(tasks[0])), "task.yaml"))
	if err != nil || !strings.Contains(string(raw), "dockerfile: environment/Dockerfile") || strings.Contains(string(raw), "image:") {
		t.Fatalf("task.yaml %v:\n%s", err, raw)
	}
}

type ibLayersFunc func(ctx context.Context, dir string) error

func (f ibLayersFunc) BuildLayers(ctx context.Context, dir string) error { return f(ctx, dir) }
