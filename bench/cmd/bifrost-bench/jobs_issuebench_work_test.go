package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/valyala/fasthttp"
)

// Lane W06: issuebench.work on a real coordinator with an in-process
// worker whose job executor is a fake.

const (
	ibwWorkerToken = "worker-secret-0123456789abcdef"
	ibwGitHubToken = "ghp_FAKEtoken0123456789abcdefXYZ"
)

// ibwExec is a fake JobExecutor: it records the lease, logs every secret
// it got (the server must redact them) and returns result, or blocks.
type ibwExec struct {
	mu      sync.Mutex
	leases  []schemas.JobLease
	result  json.RawMessage
	fail    string
	block   bool
	claimed chan struct{}
	once    sync.Once
}

func (f *ibwExec) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*engine.JobRun, error) {
	f.mu.Lock()
	f.leases = append(f.leases, *job)
	f.mu.Unlock()
	f.once.Do(func() {
		if f.claimed != nil {
			close(f.claimed)
		}
	})
	dir, err := os.MkdirTemp("", "ibwjob-*")
	if err != nil {
		return nil, err
	}
	run := &engine.JobRun{Dir: dir, Cleanup: func() { os.RemoveAll(dir) }}
	for k, v := range job.Secrets {
		log("secret " + k + "=" + v)
	}
	log("running " + strings.Join(job.Argv, " "))
	if f.block {
		<-ctx.Done()
		return run, ctx.Err()
	}
	if f.fail != "" {
		return run, errors.New(f.fail)
	}
	run.Result = f.result
	return run, nil
}

func (f *ibwExec) lease(t *testing.T) schemas.JobLease {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.leases) != 1 {
		t.Fatalf("want one lease, got %d", len(f.leases))
	}
	return f.leases[0]
}

func ibwFastTiming(t *testing.T) {
	t.Helper()
	hb, stale, claim := engine.JobHeartbeatEvery, engine.JobStaleAfter, engine.JobClaimWait
	engine.JobHeartbeatEvery, engine.JobStaleAfter, engine.JobClaimWait = 20*time.Millisecond, 5*time.Second, 5*time.Second
	t.Cleanup(func() { engine.JobHeartbeatEvery, engine.JobStaleAfter, engine.JobClaimWait = hb, stale, claim })
}

// ibwCoordinator is a coordinator served over the worker routes, plus an
// in-process worker taking only issuebench.work when exec is set.
func ibwCoordinator(t *testing.T, exec *ibwExec) *engine.Coordinator {
	t.Helper()
	ibwFastTiming(t)
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &schemas.Registry{Store: st}
	c, err := engine.NewCoordinator(reg, engine.RemoteOptions{Token: ibwWorkerToken})
	if err != nil {
		t.Fatal(err)
	}
	if exec == nil {
		return c
	}
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, c).RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: 64 << 20}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	root := t.TempDir()
	wst, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wreg := &schemas.Registry{Store: wst}
	w, err := engine.NewWorker(wreg, engine.NewPipeline(wreg, root, nil), engine.NewPlanner(wreg, root, nil).Plan,
		engine.WorkerOptions{Server: "http://" + ln.Addr().String(), Token: ibwWorkerToken, WorkerID: "shared-1", Version: engine.BenchVersion,
			PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir(), Jobs: exec, JobKinds: []string{issuebench.WorkJobKind}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(5 * time.Second)
	for !c.HasJobRunnerFor(issuebench.WorkJobKind) {
		if time.Now().After(deadline) {
			t.Fatal("the worker never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return c
}

// ibwFixture is a hosted env with the org's openai key, a fake gateway and
// a coordinator.
func ibwFixture(t *testing.T, exec *ibwExec, public bool, github bool) (jobEnv, keystore.Store, *keystore.Key, *orgCalGateway) {
	t.Helper()
	env, ks, key, gw, _ := hostedCalibrateFixture(t)
	if github {
		if _, err := ks.Create(context.Background(), "org1", keystore.KindSecret, keystore.Put{Provider: auth.GitHubRepoProvider, Label: "gh", Value: ibwGitHubToken}, "u"); err != nil {
			t.Fatal(err)
		}
	}
	old := ibRepoPublic
	ibRepoPublic = func(context.Context, work.Git, string) bool { return public }
	t.Cleanup(func() { ibRepoPublic = old })
	env.Services.Launcher = &launcher{a: env.App, org: "org1", coord: ibwCoordinator(t, exec)}
	return env, ks, key, gw
}

func ibwServerRequest(mode string) issuebench.WorkRequest {
	r := issuebench.WorkRequest{
		Mode: mode, OrgID: "someone-else", Repo: "acme/widgets", Version: "2026-10",
		Candidate: issuebench.Candidate{Repo: "acme/widgets", Issue: 42, BaseSHA: strings.Repeat("a", 40), TestPatch: "diff test", FixPatch: "diff fix"},
		Provider:  "openai", Model: "judge-model", MaxUSD: 2,
	}
	if mode == issuebench.WorkModeValidate {
		r.Env = &issuebench.EnvSpec{Dockerfile: "FROM python:3.12"}
		r.Provider, r.Model, r.MaxUSD = "", "", 0 // W07 sends the key id only
	}
	return r
}

func ibwParams(t *testing.T, env jobEnv, mode string) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(issuebench.WorkParams{Request: ibwServerRequest(mode), Consent: true})
	norm, target, err := validateIssuebenchWork(context.Background(), env, raw)
	if err != nil {
		t.Fatal(err)
	}
	if target != "acme/widgets#42" {
		t.Fatalf("target %q", target)
	}
	out, _ := json.Marshal(norm)
	return out
}

func ibwRun(ctx context.Context, env jobEnv, params json.RawMessage) (any, string, []any, error) {
	log := &ibwLog{}
	var prog []any
	res, err := runIssuebenchWork(ctx, env, &jobs.Job{ID: "job_IBW1", Params: params}, log, func(v any) { prog = append(prog, v) })
	return res, log.String(), prog, err
}

// ibwLog is a concurrency safe jobs.Logger.
type ibwLog struct {
	mu sync.Mutex
	testJobLog
}

func (l *ibwLog) Printf(f string, a ...any) { l.mu.Lock(); l.testJobLog.Printf(f, a...); l.mu.Unlock() }
func (l *ibwLog) Warnf(f string, a ...any)  { l.mu.Lock(); l.testJobLog.Warnf(f, a...); l.mu.Unlock() }
func (l *ibwLog) Errorf(f string, a ...any) { l.mu.Lock(); l.testJobLog.Errorf(f, a...); l.mu.Unlock() }
func (l *ibwLog) String() string            { l.mu.Lock(); defer l.mu.Unlock(); return l.testJobLog.String() }

func ibwRequestFile(t *testing.T, files []byte) (issuebench.WorkRequest, string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(files), int64(len(files)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != issuebench.WorkRequestFile {
		t.Fatalf("snapshot files %v", zr.File)
	}
	rc, _ := zr.File[0].Open()
	body, _ := io.ReadAll(rc)
	rc.Close()
	var req issuebench.WorkRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return req, string(body)
}

func TestIssuebenchWorkBuildPrivate(t *testing.T) {
	exec := &ibwExec{result: json.RawMessage(`{"mode":"build","cost_usd":0.75,"seconds":12,"validation":{}}`)}
	env, ks, key, gw := ibwFixture(t, exec, false, true)
	params := ibwParams(t, env, issuebench.WorkModeBuild)
	res, log, prog, err := ibwRun(context.Background(), env, params)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	r := res.(issuebench.WorkResult)
	if r.Worker != "shared-1" || r.CostUSD != 0.75 || r.Mode != issuebench.WorkModeBuild {
		t.Fatalf("result %+v", r)
	}
	l := exec.lease(t)
	if l.JobKind != issuebench.WorkJobKind || strings.Join(l.Argv, " ") != strings.Join(issuebench.WorkArgv(), " ") || len(l.Collect) != 0 {
		t.Fatalf("lease %+v", l.Redacted())
	}
	if l.Secrets[issuebench.SecretGitHubToken] != ibwGitHubToken || l.Secrets[issuebench.SecretGatewayKey] != "sk-judge" || l.Secrets[issuebench.SecretGatewayURL] == "" {
		t.Fatalf("secrets not delivered: %v", l.Redacted().Secrets)
	}
	req, body := ibwRequestFile(t, l.Files)
	if req.JobID != "job_IBW1" || req.OrgID != "org1" || !req.Private || req.Mode != issuebench.WorkModeBuild {
		t.Fatalf("request %+v", req)
	}
	for _, where := range map[string]string{"log": log, "params": string(params), "request.json": body} {
		if strings.Contains(where, ibwGitHubToken) || strings.Contains(where, "sk-judge") || strings.Contains(where, "sk-org-own") {
			t.Fatalf("a secret leaked:\n%s", where)
		}
	}
	if !strings.Contains(log, "secret github_token=[redacted]") || !strings.Contains(log, "running issuebench work") {
		t.Fatalf("worker log lines not relayed redacted:\n%s", log)
	}
	gw.mu.Lock()
	minted, revoked := gw.minted, gw.revoked
	gw.mu.Unlock()
	if minted != 1 || revoked != 1 {
		t.Fatalf("minted %d revoked %d", minted, revoked)
	}
	k, _ := ks.Get(context.Background(), "org1", key.ID)
	if k.MonthSpentUSD != 0.75 {
		t.Fatalf("spend %v", k.MonthSpentUSD)
	}
	if len(prog) != 1 || prog[0].(map[string]any)["cost_usd"] != 0.75 {
		t.Fatalf("progress %v", prog)
	}
}

func TestIssuebenchWorkPublicValidateNoToken(t *testing.T) {
	exec := &ibwExec{result: json.RawMessage(`{"mode":"validate","cost_usd":0,"validation":{}}`)}
	env, _, _, gw := ibwFixture(t, exec, true, true)
	if _, log, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeValidate)); err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	l := exec.lease(t)
	if len(l.Secrets) != 0 {
		t.Fatalf("a public validate job got secrets: %v", l.Redacted().Secrets)
	}
	if req, _ := ibwRequestFile(t, l.Files); req.Private {
		t.Fatal("public repository marked private")
	}
	if gw.minted != 0 {
		t.Fatal("a validate job minted a key")
	}
}

func TestIssuebenchWorkPublicBuildNoGitHubToken(t *testing.T) {
	exec := &ibwExec{result: json.RawMessage(`{"mode":"build","cost_usd":0.1}`)}
	env, _, _, _ := ibwFixture(t, exec, true, true)
	if _, log, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild)); err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	l := exec.lease(t)
	if _, ok := l.Secrets[issuebench.SecretGitHubToken]; ok {
		t.Fatal("a public repository got the GitHub token")
	}
	if l.Secrets[issuebench.SecretGatewayKey] == "" {
		t.Fatal("no gateway key")
	}
}

func TestIssuebenchWorkPrivateWithoutGitHub(t *testing.T) {
	exec := &ibwExec{}
	env, _, _, gw := ibwFixture(t, exec, false, false)
	_, _, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild))
	if err == nil || !strings.Contains(err.Error(), "Settings > GitHub") {
		t.Fatalf("private without GitHub: %v", err)
	}
	if gw.minted != 0 || len(exec.leases) != 0 {
		t.Fatal("work started without repository access")
	}
}

func TestIssuebenchWorkRevokesOnCancel(t *testing.T) {
	exec := &ibwExec{block: true, claimed: make(chan struct{})}
	env, _, _, gw := ibwFixture(t, exec, false, true)
	params := ibwParams(t, env, issuebench.WorkModeBuild)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, _, _, err := ibwRun(ctx, env, params); errc <- err }()
	select {
	case <-exec.claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("never claimed")
	}
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("cancelled job succeeded")
	}
	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.minted != 1 || gw.revoked != 1 {
		t.Fatalf("cancel: minted %d revoked %d", gw.minted, gw.revoked)
	}
}

func TestIssuebenchWorkNoRunner(t *testing.T) {
	env, _, _, gw := ibwFixture(t, nil, true, false)
	_, _, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild))
	if err == nil || !strings.HasPrefix(err.Error(), "No worker online:") {
		t.Fatalf("no runner: %v", err)
	}
	env.Services.Launcher = nil
	if _, _, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild)); err == nil || !strings.HasPrefix(err.Error(), "No worker online:") {
		t.Fatalf("no launcher: %v", err)
	}
	if gw.minted != 0 {
		t.Fatal("minted with no runner")
	}
}

func TestIssuebenchWorkResultOverCap(t *testing.T) {
	big := `{"mode":"build","stop":"` + strings.Repeat("x", issuebench.MaxWorkResultBytes) + `"}`
	exec := &ibwExec{result: json.RawMessage(big)}
	env, _, _, gw := ibwFixture(t, exec, true, false)
	_, _, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild))
	if err == nil || !strings.Contains(err.Error(), "byte") {
		t.Fatalf("over cap: %v", err)
	}
	if gw.revoked != 1 {
		t.Fatal("key not revoked after a failed result")
	}
}

func TestIssuebenchWorkFailureRedacted(t *testing.T) {
	exec := &ibwExec{fail: "fetch failed with " + ibwGitHubToken}
	env, _, _, _ := ibwFixture(t, exec, false, true)
	_, _, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild))
	if err == nil || strings.Contains(err.Error(), ibwGitHubToken) {
		t.Fatalf("failure: %v", err)
	}
}

func TestIssuebenchWorkValidate(t *testing.T) {
	env := jobEnv{OrgID: "org1", Hosted: true}
	bad := []issuebench.WorkParams{
		{Request: ibwServerRequest(issuebench.WorkModeBuild)}, // no consent
		{Request: issuebench.WorkRequest{Mode: "x"}, Consent: true},
		{Request: func() issuebench.WorkRequest { r := ibwServerRequest(issuebench.WorkModeValidate); r.Env = nil; return r }(), Consent: true},
	}
	for _, p := range bad {
		raw, _ := json.Marshal(p)
		if _, _, err := validateIssuebenchWork(context.Background(), env, raw); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	raw, _ := json.Marshal(issuebench.WorkParams{Request: ibwServerRequest(issuebench.WorkModeBuild), Consent: true})
	norm, _, err := validateIssuebenchWork(context.Background(), env, raw)
	if err != nil {
		t.Fatal(err)
	}
	if p := norm.(issuebench.WorkParams); p.Request.OrgID != "org1" || p.Request.JobID != "" {
		t.Fatalf("org not forced: %+v", p.Request)
	}
	spec, ok := runnerJobHostedSpec(issuebench.WorkJobKind)
	if !ok || spec.Local {
		t.Fatalf("issuebench.work must be hosted only: %+v %v", spec, ok)
	}
}

func TestIssuebenchWorkReadiness(t *testing.T) {
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "openai")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "m")
	local := issuebenchReadiness(context.Background(), handlers.OrgEnv{Cfg: &lib.Config{}, Services: handlers.Services{OrgID: "org1"}})
	if local.Hosted || !local.WorkerOnline || local.Gateway || !local.DefaultModel || local.Message != "" {
		t.Fatalf("local %+v", local)
	}
	cfg := &lib.Config{}
	hosted := handlers.OrgEnv{Cfg: cfg, Services: handlers.Services{OrgID: "org1", Hosted: true, Launcher: &launcher{coord: ibwCoordinator(t, nil)}}}
	r := issuebenchReadiness(context.Background(), hosted)
	if r.WorkerOnline || !strings.HasPrefix(r.Message, "No worker online:") {
		t.Fatalf("no worker %+v", r)
	}
	hosted.Services.Launcher = &launcher{coord: ibwCoordinator(t, &ibwExec{})}
	ks := keystore.NewMemory()
	if _, err := ks.Create(context.Background(), "org1", keystore.KindSecret, keystore.Put{Provider: auth.GitHubRepoProvider, Label: "gh", Value: ibwGitHubToken}, "u"); err != nil {
		t.Fatal(err)
	}
	hosted.Services.Keys = ks
	r = issuebenchReadiness(context.Background(), hosted)
	if !r.WorkerOnline || !r.GitHub || r.Gateway || !strings.Contains(r.Message, "no model gateway") {
		t.Fatalf("no gateway %+v", r)
	}
	cfg.Gateway.URL = "http://bifrost:8080"
	if r = issuebenchReadiness(context.Background(), hosted); !r.Gateway || r.Message != "" {
		t.Fatalf("ready %+v", r)
	}
	if handlers.IssueBenchReadiness == nil {
		t.Fatal("readiness not wired")
	}
}
