package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	_ "github.com/maximhq/bifrost/bench/issuebench/lang/python"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/valyala/fasthttp"
)

// Lane W10: From issues on the hosted site, end to end on a fake worker.
// The server side is real (hosted job runner with the registered kinds, the
// org's engine.Coordinator, the pipeline and its stage adapters, emit); the
// worker is a real in-process engine.Worker whose job executor is the real
// ibWorkExecutor with fake docker, a fixture git repository and a scripted
// build sandbox. The builder's model is a fake Bifrost chat route.

const (
	ibeOrg         = "org1"
	ibeRepo        = "acme/widgets"
	ibeToken       = "ghp_testtoken123456"
	ibeVK          = "vk-ibe2e-secret-0123456789"
	ibeWorkerToken = "worker-secret-0123456789abcdef"
	ibeProviderKey = "sk-org-own-provider-key"
	ibeTestFile    = "tests/test_widgets.py"
)

// ibeFixture is a local git repository with a base commit and a fix commit,
// served to the worker's checkout from a bare copy.
type ibeFixture struct {
	bare       string
	base       string
	testPatch  string
	fixPatch   string
	mergedAt   time.Time
	candidate  issuebench.Candidate
	gitEnvSeen []string
	gitArgv    []string
	mu         sync.Mutex
}

func ibeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func ibeWrite(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func newIbeFixture(t *testing.T) *ibeFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(t.TempDir(), "widgets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ibeGit(t, dir, "init", "-q")
	ibeWrite(t, dir, map[string]string{
		"pyproject.toml":      "[project]\nname = \"widgets\"\nversion = \"0.1.0\"\nrequires-python = \">=3.9\"\n",
		"widgets/__init__.py": "def add(a, b):\n    return a - b\n",
		ibeTestFile:           "from widgets import add\n\n\ndef test_old():\n    assert add(0, 0) == 0\n",
		"README.md":           "Widgets: adds numbers.\n",
	})
	ibeGit(t, dir, "add", "-A")
	ibeGit(t, dir, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(ibeGit(t, dir, "rev-parse", "HEAD"))
	ibeWrite(t, dir, map[string]string{
		"widgets/__init__.py": "def add(a, b):\n    return a + b\n",
		ibeTestFile:           "from widgets import add\n\n\ndef test_old():\n    assert add(0, 0) == 0\n\n\ndef test_new():\n    assert add(2, 3) == 5\n",
	})
	ibeGit(t, dir, "add", "-A")
	ibeGit(t, dir, "commit", "-q", "-m", "fix add")
	f := &ibeFixture{
		base:      base,
		testPatch: ibeGit(t, dir, "diff", base, "HEAD", "--", "tests"),
		fixPatch:  ibeGit(t, dir, "diff", base, "HEAD", "--", "widgets"),
		mergedAt:  time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
	}
	f.bare = filepath.Join(t.TempDir(), "widgets.git")
	ibeGit(t, filepath.Dir(f.bare), "clone", "-q", "--bare", dir, f.bare)
	ibeGit(t, f.bare, "config", "uploadpack.allowAnySHA1InWant", "true")
	f.candidate = issuebench.Candidate{
		Repo: ibeRepo, PR: 12, Issue: 11, IssueTitle: "add subtracts instead of adding",
		IssueBody:    "Calling add(2, 3) gives -1. I expect the sum of both numbers.",
		IssueCreated: f.mergedAt.Add(-48 * time.Hour), PRMerged: f.mergedAt,
		BaseSHA: base, MergeSHA: strings.TrimSpace(ibeGit(t, dir, "rev-parse", "HEAD")),
		TestFiles: []string{ibeTestFile}, CodeFiles: []string{"widgets/__init__.py"},
		TestPatch: f.testPatch, FixPatch: f.fixPatch, Language: "python",
	}
	return f
}

// git is the worker's Git seam: the real ExecGit with the GitHub URL of the
// fixture rewritten to the bare copy. It records env and argv as the code
// made them, so the test sees where the token went.
func (f *ibeFixture) git(ctx context.Context, env []string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.gitEnvSeen = append(f.gitEnvSeen, env...)
	f.gitArgv = append(f.gitArgv, strings.Join(args, " "))
	f.mu.Unlock()
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "https://github.com/"+ibeRepo+".git", "file://"+f.bare)
	}
	return work.ExecGit(ctx, env, out...)
}

// ibePytest is the -rA summary pytest would print for the fixture: test_old
// always passes, test_new exists once the test patch is in and passes once
// the fix is in.
func ibePytest(hasNewTest, fixed bool) string {
	var b strings.Builder
	b.WriteString("============ short test summary info ============\n")
	b.WriteString("PASSED " + ibeTestFile + "::test_old\n")
	if hasNewTest {
		if fixed {
			b.WriteString("PASSED " + ibeTestFile + "::test_new\n")
		} else {
			b.WriteString("FAILED " + ibeTestFile + "::test_new - assert -1 == 5\n")
		}
	}
	return b.String()
}

// ibeSandbox is a scripted build sandbox: a virtual copy of the repository
// that the builder's state overlays write into, and pytest answered from it.
type ibeSandbox struct {
	mu    sync.Mutex
	files map[string]string
	cmds  *[]string
}

var (
	ibeOverlayWrite = regexp.MustCompile(`echo (\S+) \| base64 -d > '` + builder.Workdir + `/([^']+)'`)
	ibeOverlayRm    = regexp.MustCompile(`rm -f '` + builder.Workdir + `/([^']+)'`)
)

func (s *ibeSandbox) Exec(_ context.Context, cmd string, _ time.Duration) (int, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.cmds = append(*s.cmds, cmd)
	if ms := ibeOverlayWrite.FindAllStringSubmatch(cmd, -1); len(ms) > 0 {
		for _, m := range ms {
			b, err := base64.StdEncoding.DecodeString(m[1])
			if err != nil {
				return 1, "bad base64", nil
			}
			s.files[m[2]] = string(b)
		}
		for _, m := range ibeOverlayRm.FindAllStringSubmatch(cmd, -1) {
			delete(s.files, m[1])
		}
		return 0, "", nil
	}
	if strings.Contains(cmd, "pytest") {
		return 1, ibePytest(strings.Contains(s.files[ibeTestFile], "def test_new"), strings.Contains(s.files["widgets/__init__.py"], "a + b")), nil
	}
	if strings.HasPrefix(cmd, "head -c") {
		return 0, "", nil
	}
	return 0, "ok\n", nil
}
func (s *ibeSandbox) Snapshot(context.Context) (string, error) { return "snap", nil }
func (s *ibeSandbox) Rollback(context.Context, string) error   { return nil }
func (s *ibeSandbox) Close(context.Context) error              { return nil }

// ibeWorker is everything the fake worker recorded.
type ibeWorker struct {
	mu        sync.Mutex
	docker    []string // argv of every docker call
	stdin     []string // stdin of every docker call
	sandboxes int
	cmds      []string // sandbox commands
	logs      []string // lines the executor handed to the coordinator
}

func (w *ibeWorker) dockerFunc(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	w.mu.Lock()
	w.docker = append(w.docker, strings.Join(args, " "))
	w.stdin = append(w.stdin, string(stdin))
	w.mu.Unlock()
	if len(args) == 0 {
		return nil, fmt.Errorf("no docker args")
	}
	switch args[0] {
	case "run":
		script := string(stdin)
		return []byte(ibePytest(strings.Contains(script, "/tmp/tests.patch"), strings.Contains(script, "/tmp/fix.patch"))), &issuebench.ExitError{Code: 1}
	case "build":
		return []byte("sha256:0123456789abcdef\n"), nil
	}
	return nil, nil
}

func (w *ibeWorker) factory(_ context.Context, _ string, hostRepoDir string, _ []string) (builder.Sandbox, error) {
	files := map[string]string{}
	err := filepath.WalkDir(hostRepoDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(hostRepoDir, p)
			files[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.sandboxes++
	w.mu.Unlock()
	return &ibeSandbox{files: files, cmds: &w.cmds}, nil
}

// ibeGateway is the server's gateway: it records mint and revoke and binds
// org keys like the hosted Bifrost client.
type ibeGateway struct {
	mu      sync.Mutex
	minted  []string
	revoked []string
	creds   []schemas.ProviderCredential
	bound   map[string]string
}

func (g *ibeGateway) GetName() string    { return "fake" }
func (g *ibeGateway) GetVersion() string { return "0" }
func (g *ibeGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return &schemas.GatewayManifest{}, nil
}
func (g *ibeGateway) MintKey(_ context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bound[rc.Run.ExpID] == "" {
		return nil, fmt.Errorf("key not bound to an org")
	}
	g.minted = append(g.minted, rc.Run.RunID)
	return &schemas.RunKey{ID: "vkid-" + rc.Run.RunID, Value: ibeVK}, nil
}
func (g *ibeGateway) RevokeKey(_ context.Context, k *schemas.RunKey) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.revoked = append(g.revoked, k.ID)
	return nil
}
func (g *ibeGateway) RevokeStale(context.Context, map[string]bool) (int, error) { return 0, nil }
func (g *ibeGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return nil, fmt.Errorf("not used")
}
func (g *ibeGateway) ApplyOrgCredentials(_ context.Context, _ string, creds []schemas.ProviderCredential) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.creds = append(g.creds, creds...)
	return nil
}
func (g *ibeGateway) BindExperimentOrg(exp, org string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bound == nil {
		g.bound = map[string]string{}
	}
	g.bound[exp] = org
}

// ibeChat is Bifrost's chat route. A request with tools is the builder: it
// installs, runs the tests at gold, then submits. A request without tools
// is a model check: a clear issue with no leak, then two wrong patches.
func ibeChat(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []json.RawMessage `json:"tools"`
		}
		_ = json.Unmarshal(raw, &req)
		mu.Lock()
		keys = append(keys, r.Header.Get("x-bf-vk"))
		mu.Unlock()
		if r.Header.Get("x-bf-vk") != ibeVK {
			http.Error(w, "unknown virtual key", http.StatusUnauthorized)
			return
		}
		msg := map[string]any{"role": "assistant"}
		if len(req.Tools) > 0 {
			turns := 0
			for _, m := range req.Messages {
				if m.Role == "assistant" {
					turns++
				}
			}
			call := func(name string, args any) []any {
				b, _ := json.Marshal(args)
				return []any{map[string]any{"id": fmt.Sprintf("c%d", turns), "type": "function",
					"function": map[string]any{"name": name, "arguments": string(b)}}}
			}
			switch turns {
			case 0:
				msg["tool_calls"] = call("bash", map[string]any{"cmd": "pip install -e ."})
			case 1:
				msg["tool_calls"] = call("run_tests", map[string]any{"state": "gold"})
			default:
				msg["tool_calls"] = call("submit", map[string]any{})
			}
			msg["content"] = ""
		} else {
			last := req.Messages[len(req.Messages)-1].Content
			msg["content"] = `{"leak":false,"leak_evidence":"","clarity":1,"clarity_reason":"says what add must return"}`
			if strings.Contains(last, "near-miss wrong patches") {
				p1 := "diff --git a/widgets/__init__.py b/widgets/__init__.py\n--- a/widgets/__init__.py\n+++ b/widgets/__init__.py\n@@ -1,2 +1,2 @@\n def add(a, b):\n-    return a - b\n+    return a * b\n"
				p2 := "diff --git a/widgets/__init__.py b/widgets/__init__.py\n--- a/widgets/__init__.py\n+++ b/widgets/__init__.py\n@@ -1,2 +1,2 @@\n def add(a, b):\n-    return a - b\n+    return b - a\n"
				b, _ := json.Marshal(map[string]any{"patches": []any{map[string]string{"idea": "multiply", "diff": p1}, map[string]string{"idea": "swap", "diff": p2}}})
				msg["content"] = string(b)
			}
		}
		out, _ := json.Marshal(map[string]any{"model": "judge-model", "choices": []any{map[string]any{"message": msg}},
			"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 100}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv, &keys
}

// ibeLogger keeps every coordinator log line.
type ibeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *ibeLogger) add(level, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprint(append([]any{level, msg}, args...)...))
}
func (l *ibeLogger) Debug(m string, a ...any) { l.add("debug", m, a...) }
func (l *ibeLogger) Info(m string, a ...any)  { l.add("info", m, a...) }
func (l *ibeLogger) Warn(m string, a ...any)  { l.add("warn", m, a...) }
func (l *ibeLogger) Error(m string, a ...any) { l.add("error", m, a...) }
func (l *ibeLogger) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// ibeServer is the hosted server of one org.
type ibeServer struct {
	root    string
	env     jobEnv
	oenv    handlers.OrgEnv
	runner  *jobs.Runner
	coord   *engine.Coordinator
	coordLg *ibeLogger
	url     string
	gw      *ibeGateway
	ks      keystore.Store
	admit   *[]string
	amu     *sync.Mutex
}

func newIbeServer(t *testing.T, chatURL string) *ibeServer {
	t.Helper()
	root := t.TempDir()
	s := &ibeServer{root: root, gw: &ibeGateway{}, coordLg: &ibeLogger{}, admit: &[]string{}, amu: &sync.Mutex{}}
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.coord, err = engine.NewCoordinator(&schemas.Registry{Store: st}, engine.RemoteOptions{Token: ibeWorkerToken, Logger: s.coordLg})
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, s.coord).RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: 64 << 20}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	s.url = "http://" + ln.Addr().String()

	s.ks = keystore.NewMemory()
	ctx := context.Background()
	if _, err := s.ks.Create(ctx, ibeOrg, keystore.KindProvider, keystore.Put{Provider: "openai", Label: "mine", Value: ibeProviderKey}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ks.Create(ctx, ibeOrg, keystore.KindSecret, keystore.Put{Provider: auth.GitHubRepoProvider, Label: "github", Value: ibeToken}, "u"); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root}
	cfg.Gateway.URL = chatURL
	cfg.Gateway.PublicURL = chatURL
	a := &app{cfg: cfg}
	l := &launcher{a: a, org: ibeOrg, coord: s.coord, remoteOnly: true}
	s.runner = jobs.New(dbtest.Open(t), jobs.Options{Owner: "ibe2e", PollEvery: 10 * time.Millisecond, HeartbeatEvery: 50 * time.Millisecond,
		LeaseFor: 5 * time.Second, ShutdownGrace: 5 * time.Second})
	svc := handlers.Services{OrgID: ibeOrg, Hosted: true, Keys: s.ks, Jobs: s.runner, Launcher: l}
	s.env = jobEnv{App: a, OrgID: ibeOrg, ProjectRoot: root, Hosted: true, Services: svc}
	s.oenv = handlers.OrgEnv{Cfg: cfg, Mode: schemas.ModeLive, Services: svc}
	for _, kind := range []string{issuebench.WorkJobKind, kindIssuebenchCheck} {
		spec, ok := runnerJobHostedSpec(kind)
		if !ok {
			t.Fatalf("no hosted %s kind is registered", kind)
		}
		opts := append([]jobs.KindOption(nil), spec.Opts...)
		if spec.Validate != nil {
			opts = append(opts, jobs.WithValidator(func(ctx context.Context, _ string, raw json.RawMessage) (any, string, error) {
				return spec.Validate(ctx, s.env, raw)
			}))
		}
		s.runner.Register(kind, func(ctx context.Context, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
			return runRedacted(ctx, spec, s.env, j, log, progress)
		}, opts...)
	}
	for _, kind := range pipeline.AdmitKinds { // readiness runs on a worker in real life; here it only has to be queued
		kind := kind
		s.runner.Register(kind, func(_ context.Context, j *jobs.Job, _ jobs.Logger, _ func(any)) (any, error) {
			s.amu.Lock()
			*s.admit = append(*s.admit, kind+" "+string(j.Params))
			s.amu.Unlock()
			return map[string]bool{"ok": true}, nil
		})
	}
	rctx, cancel := context.WithCancel(context.Background())
	if err := s.runner.Start(rctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); s.runner.Stop(context.Background()) })
	return s
}

// startWorker runs the shared-style worker (it takes issuebench.work only).
func (s *ibeServer) startWorker(t *testing.T, f *ibeFixture, w *ibeWorker) {
	t.Helper()
	wst, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &schemas.Registry{Store: wst}
	wroot := t.TempDir()
	x := &ibWorkExecutor{
		org: ibeOrg,
		sem: make(chan struct{}, 1),
		newDeps: func(dir string) work.Deps {
			return work.Deps{
				Docker:   w.dockerFunc,
				Checkout: work.Checkouter{Git: f.git}.Checkout,
				Sandbox: func(context.Context, issuebench.WorkRequest) (builder.SandboxFactory, func(), error) {
					return w.factory, func() {}, nil
				},
				Scratch: dir,
				Log:     func(format string, args ...any) {},
			}
		},
	}
	wk, err := engine.NewWorker(reg, engine.NewPipeline(reg, wroot, nil), engine.NewPlanner(reg, wroot, nil).Plan, engine.WorkerOptions{
		Server: s.url, Token: ibeWorkerToken, WorkerID: "shared-1", PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir(),
		Jobs: ibeLogTap{x: x, w: w}, JobKinds: []string{issuebench.WorkJobKind},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = wk.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(10 * time.Second)
	for !s.coord.HasJobRunnerFor(issuebench.WorkJobKind) {
		if time.Now().After(deadline) {
			t.Fatal("the worker did not register")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ibeLogTap keeps the lines the executor sends to the coordinator.
type ibeLogTap struct {
	x *ibWorkExecutor
	w *ibeWorker
}

func (t ibeLogTap) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*engine.JobRun, error) {
	return t.x.RunJob(ctx, job, func(line string) {
		t.w.mu.Lock()
		t.w.logs = append(t.w.logs, line)
		t.w.mu.Unlock()
		log(line)
	})
}

// manager is the org's pipeline with the real stage adapters and a fake
// scanner that returns the fixture's one candidate.
func (s *ibeServer) manager(f *ibeFixture) *pipeline.Manager {
	return handlers.IssueBenchManager(s.root, func() pipeline.Stages {
		st := issuebenchStagesFor(s.oenv)
		st.Scanner = ibeScanner{c: f.candidate}
		return st
	})
}

type ibeScanner struct{ c issuebench.Candidate }

func (s ibeScanner) Scan(_ context.Context, repo string, _ pipeline.ScanOptions) ([]issuebench.Candidate, issuebench.Funnel, error) {
	c := s.c
	c.Repo = repo
	return []issuebench.Candidate{c}, issuebench.Funnel{MergedPRs: 1, Linked: 1, Qualified: 1}, nil
}

// ibeServerGit is the server's command seam for emit's checkout: git runs
// for real against the fixture, docker never runs on the hosted server.
func ibeServerGit(t *testing.T, f *ibeFixture, calls *[]string, mu *sync.Mutex) {
	t.Helper()
	old := ibCommand
	ibCommand = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		mu.Lock()
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		mu.Unlock()
		if name != "git" {
			return []byte(name + " is not allowed on the hosted server"), fmt.Errorf("%s refused", name)
		}
		out := make([]string, len(args))
		for i, a := range args {
			out[i] = strings.ReplaceAll(a, "https://github.com/"+ibeRepo+".git", f.bare)
		}
		return old(ctx, stdin, name, out...)
	}
	t.Cleanup(func() { ibCommand = old })
}

func ibeWait(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ibeSeams makes the job timing fast and points the server's gateway at
// the fake.
func ibeSeams(t *testing.T, gw *ibeGateway, chatURL string) {
	t.Helper()
	hb, stale, claim := engine.JobHeartbeatEvery, engine.JobStaleAfter, engine.JobClaimWait
	engine.JobHeartbeatEvery, engine.JobStaleAfter, engine.JobClaimWait = 20*time.Millisecond, 10*time.Second, 10*time.Second
	oldPoll, oldGW, oldPublic := ibPoll, calibrateGatewayFor, ibRepoPublic
	ibPoll = 10 * time.Millisecond
	calibrateGatewayFor = func(context.Context, jobEnv) (schemas.Gateway, string, func(), error) {
		return gw, chatURL, func() {}, nil
	}
	ibRepoPublic = func(context.Context, work.Git, string) bool { return false } // private: the token travels
	t.Cleanup(func() {
		engine.JobHeartbeatEvery, engine.JobStaleAfter, engine.JobClaimWait = hb, stale, claim
		ibPoll, calibrateGatewayFor, ibRepoPublic = oldPoll, oldGW, oldPublic
	})
	t.Setenv("BENCH_ISSUEBENCH_PROVIDER", "openai")
	t.Setenv("BENCH_ISSUEBENCH_MODEL", "judge-model")
	t.Setenv("BENCH_ISSUEBENCH_MAX_USD", "2")
	t.Setenv("GITHUB_TOKEN", "")
}

// ibeStartRoute calls POST .../runs/{id}/start through the org's real routes.
func ibeStartRoute(t *testing.T, oenv handlers.OrgEnv, id string) (int, string) {
	t.Helper()
	r := router.New()
	for _, area := range handlers.OrgAreas() {
		area(oenv, r)
	}
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(handlers.APIPrefix + "/issuebench/runs/" + id + "/start")
	ctx.Request.SetBodyString(`{"consent":true,"provider":"openai","model":"judge-model"}`)
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

func TestIssuebenchHostedEndToEndOnFakeWorker(t *testing.T) {
	t.Run("no worker", ibeNoWorker)
	f := newIbeFixture(t)
	chat, chatKeys := ibeChat(t)
	s := newIbeServer(t, chat.URL)
	ibeSeams(t, s.gw, chat.URL)
	var serverCalls []string
	var serverMu sync.Mutex
	ibeServerGit(t, f, &serverCalls, &serverMu)
	w := &ibeWorker{}
	s.startWorker(t, f, w)

	ctx := context.Background()
	m := s.manager(f)
	st, err := m.Scan(ctx, ibeRepo, pipeline.ScanOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := ibeStartRoute(t, s.oenv, st.ID); code != http.StatusAccepted {
		t.Fatalf("start with a worker online: %d %s", code, body)
	}
	var cur *pipeline.State
	ibeWait(t, "the issue to be ready", 60*time.Second, func() bool {
		cur, err = m.Load(st.ID)
		return err == nil && len(cur.Items) == 1 && (cur.Items[0].Status == pipeline.StatusReady || cur.Items[0].Status == pipeline.StatusFailed)
	})
	m.Wait()
	cur, _ = m.Load(st.ID)
	it := cur.Items[0]
	logText := func(s *pipeline.State) string {
		var b strings.Builder
		for _, l := range s.Log {
			b.WriteString(l.Line + "\n")
		}
		return b.String()
	}
	if it.Status != pipeline.StatusReady {
		t.Fatalf("item %s: %s (fix %q)\n%s", it.Status, it.Reason, it.Fix, logText(cur))
	}
	if it.JobID == "" || it.Stage != issuebench.StageChecks {
		t.Fatalf("item job %q stage %q, want a job and stage checks", it.JobID, it.Stage)
	}
	if v := it.Validation; v == nil || len(v.FailToPass) != 1 || !strings.HasSuffix(v.FailToPass[0], "test_new") ||
		len(v.PassToPass) != 1 || !v.Checks.Clear || v.Checks.Leak {
		t.Fatalf("validation %+v", it.Validation)
	}
	lt := logText(cur)
	for _, want := range []string{"build runs as job", "checks runs as job"} {
		if !strings.Contains(lt, want) {
			t.Errorf("item log lacks %q:\n%s", want, lt)
		}
	}

	t.Logf("item log:\n%s", lt)
	// The build and test ran as one issuebench.work job on the worker.
	page, err := s.runner.List(ctx, ibeOrg, jobs.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var workJobs []jobs.Job
	for _, j := range page.Jobs {
		if j.Kind == issuebench.WorkJobKind {
			workJobs = append(workJobs, j)
		}
	}
	if len(workJobs) != 1 || workJobs[0].Status != jobs.StatusSucceeded {
		t.Fatalf("work jobs %+v", workJobs)
	}
	var wres issuebench.WorkResult
	if err := json.Unmarshal(workJobs[0].Result, &wres); err != nil || wres.Worker != "shared-1" || wres.Build == nil ||
		wres.Build.Stop != issuebench.StopOK || wres.Validation == nil {
		t.Fatalf("work result %s: %v", workJobs[0].Result, err)
	}

	// Key minted on the server's gateway with the org's own key, and revoked.
	s.gw.mu.Lock()
	minted, revoked, creds := append([]string(nil), s.gw.minted...), append([]string(nil), s.gw.revoked...), len(s.gw.creds)
	s.gw.mu.Unlock()
	var buildKeys []string
	for _, k := range minted {
		if strings.HasPrefix(k, "r_ibw") {
			buildKeys = append(buildKeys, k)
		}
	}
	if len(buildKeys) != 1 || !strings.Contains(strings.Join(revoked, " "), "vkid-"+buildKeys[0]) || len(revoked) != len(minted) || creds == 0 {
		t.Fatalf("gateway minted %v revoked %v creds %d", minted, revoked, creds)
	}
	for _, k := range *chatKeys {
		if k != ibeVK {
			t.Fatalf("a model call used key %q", k)
		}
	}

	// The token reached git only through env, never argv.
	f.mu.Lock()
	gitEnv, gitArgv := strings.Join(f.gitEnvSeen, "\n"), strings.Join(f.gitArgv, "\n")
	f.mu.Unlock()
	if !strings.Contains(gitEnv, base64.StdEncoding.EncodeToString([]byte("x-access-token:"+ibeToken))) || strings.Contains(gitArgv, ibeToken) {
		t.Fatalf("token: env has it %v, argv has it %v", strings.Contains(gitEnv, "x-access-token"), strings.Contains(gitArgv, ibeToken))
	}
	w.mu.Lock()
	dockerText := strings.Join(w.docker, "\n")
	workerLogs := strings.Join(w.logs, "\n")
	sandboxes := w.sandboxes
	w.mu.Unlock()
	if sandboxes == 0 || !strings.Contains(dockerText, "run --rm -i --network none") || !strings.Contains(dockerText, "build --quiet -t heimdall-issuebench/") {
		t.Fatalf("worker docker calls:\n%s", dockerText)
	}

	// Accept: emit on the server writes a self-contained task and queues
	// lint, lock and admit.
	if _, err := m.Review(ctx, st.ID, f.candidate.Issue, pipeline.Review{Decision: "accept"}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	cur, _ = m.Load(st.ID)
	if cur.Benchmark == "" {
		t.Fatalf("no benchmark after accept: %+v", cur)
	}
	var taskYAML string
	_ = filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "task.yaml" && strings.Contains(p, cur.Benchmark) {
			taskYAML = p
		}
		return nil
	})
	if taskYAML == "" {
		t.Fatalf("no task.yaml for %s under %s", cur.Benchmark, s.root)
	}
	taskDir := filepath.Dir(taskYAML)
	for _, p := range []string{"environment/Dockerfile", "environment/repo.tar.gz"} {
		if fi, err := os.Stat(filepath.Join(taskDir, p)); err != nil || fi.Size() == 0 {
			t.Fatalf("task misses %s: %v", p, err)
		}
	}
	ty, _ := os.ReadFile(taskYAML)
	if !regexp.MustCompile(`(?m)^review:\s*pending`).Match(ty) || regexp.MustCompile(`(?m)^\s+image:`).Match(ty) ||
		!strings.Contains(string(ty), "dockerfile: environment/Dockerfile") {
		t.Fatalf("task.yaml:\n%s", ty)
	}
	ibeWait(t, "lint, lock and admit", 10*time.Second, func() bool {
		s.amu.Lock()
		defer s.amu.Unlock()
		return len(*s.admit) == 3
	})
	s.amu.Lock()
	admitText := strings.Join(*s.admit, "\n")
	s.amu.Unlock()
	for _, k := range pipeline.AdmitKinds {
		if !strings.Contains(admitText, k+" ") || !strings.Contains(admitText, cur.Benchmark) {
			t.Fatalf("readiness jobs: %s", admitText)
		}
	}
	serverMu.Lock()
	for _, c := range serverCalls {
		if !strings.HasPrefix(c, "git ") {
			t.Errorf("the hosted server ran %q", c)
		}
	}
	serverMu.Unlock()

	// No secret value anywhere: job logs, params, results, item log, the
	// coordinator log, the worker's log lines, docker argv.
	haystacks := map[string]string{"item log": logText(cur), "coordinator log": s.coordLg.text(), "worker log": workerLogs, "docker argv": dockerText}
	page, _ = s.runner.List(ctx, ibeOrg, jobs.Filter{})
	for _, j := range page.Jobs {
		lp, err := s.runner.Logs(ctx, ibeOrg, j.ID, 0, 5000)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, l := range lp.Lines {
			b.WriteString(l.Line + "\n")
		}
		haystacks["job log "+j.Kind+" "+j.ID] = b.String()
		haystacks["params "+j.Kind+" "+j.ID] = string(j.Params)
		haystacks["result "+j.Kind+" "+j.ID] = string(j.Result) + j.Error
	}
	stateRaw, _ := json.Marshal(cur)
	haystacks["run state"] = string(stateRaw)
	for name, hay := range haystacks {
		for _, secret := range []string{ibeToken, ibeVK, ibeProviderKey, base64.StdEncoding.EncodeToString([]byte("x-access-token:" + ibeToken))} {
			if strings.Contains(hay, secret) {
				t.Errorf("%s holds a secret value (%s...)", name, secret[:6])
			}
		}
	}
}

// With no worker online the hosted start answers 409, and an item that
// runs anyway fails with the open_workers fix.
func ibeNoWorker(t *testing.T) {
	f := newIbeFixture(t)
	chat, _ := ibeChat(t)
	s := newIbeServer(t, chat.URL)
	ibeSeams(t, s.gw, chat.URL)
	ctx := context.Background()
	m := s.manager(f)
	st, err := m.Scan(ctx, ibeRepo, pipeline.ScanOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	code, body := ibeStartRoute(t, s.oenv, st.ID)
	if code != http.StatusConflict || !strings.Contains(body, "No worker online") {
		t.Fatalf("start with no worker: %d %s", code, body)
	}
	if _, err := m.StartWith(st.ID, true, pipeline.ModelChoice{Provider: "openai", Model: "judge-model"}); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	cur, err := m.Load(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	it := cur.Items[0]
	if it.Status != pipeline.StatusFailed || it.Fix != pipeline.FixOpenWorkers || !strings.HasPrefix(it.Reason, "No worker online") {
		t.Fatalf("item %s reason %q fix %q", it.Status, it.Reason, it.Fix)
	}
	s.gw.mu.Lock()
	defer s.gw.mu.Unlock()
	if len(s.gw.minted) != len(s.gw.revoked) {
		t.Fatalf("minted %v revoked %v", s.gw.minted, s.gw.revoked)
	}
}
