package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	fakeToken = "ghp_testtoken123456"
	fakeVK    = "sk-bf-worker-vk-123456"
	baseSHA   = "0123456789abcdef0123456789abcdef01234567"
)

// fakeLang is a test language: a repository with runtests.sh. Its name sorts
// before python, so the builder detects it first.
type fakeLang struct{}

func (fakeLang) Name() string { return "aworkfake" }
func (fakeLang) Detect(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "runtests.sh"))
	return err == nil
}
func (fakeLang) IsTestFile(p string) bool      { return strings.HasPrefix(p, "tests/") }
func (fakeLang) BaseImages() []string          { return []string{"img:a", "img:b"} }
func (fakeLang) Hints(string) []string         { return []string{"run ./runtests.sh"} }
func (fakeLang) PinEnv(time.Time) []string     { return nil }
func (fakeLang) TestCommand(t []string) string { return "sh runtests.sh " + strings.Join(t, " ") }

var resultRe = regexp.MustCompile(`(?m)^(PASS|FAIL|ERROR) (\S+)$`)

func (fakeLang) ParseResults(out []byte) (map[string]issuebench.TestStatus, error) {
	m := map[string]issuebench.TestStatus{}
	for _, g := range resultRe.FindAllStringSubmatch(string(out), -1) {
		m[g[2]] = map[string]issuebench.TestStatus{"PASS": issuebench.TestPass, "FAIL": issuebench.TestFail, "ERROR": issuebench.TestError}[g[1]]
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("no results")
	}
	return m, nil
}

func init() { issuebench.Register(fakeLang{}) }

const runtests = `#!/bin/sh
[ -f .installed ] || { echo "ModuleNotFoundError: no module"; exit 1; }
for t in "$@"; do
  if [ ! -f "$t" ]; then continue; fi
  if grep -q fixed src/mod.txt; then echo "PASS $t::test_it"; else echo "FAIL $t::test_it"; fi
done
`

func fixture(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for p, s := range map[string]string{"runtests.sh": runtests, "src/mod.txt": "broken\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, p), []byte(s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func candidate() issuebench.Candidate {
	return issuebench.Candidate{
		Repo: "acme/tiny", PR: 7, Issue: 3, IssueTitle: "mod is broken", IssueBody: "the module is broken",
		BaseSHA: baseSHA, Language: "aworkfake",
		PRMerged:  time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		TestFiles: []string{"tests/test_mod.sh"}, CodeFiles: []string{"src/mod.txt"},
		TestPatch: "diff --git a/tests/test_mod.sh b/tests/test_mod.sh\nnew file mode 100644\n--- /dev/null\n+++ b/tests/test_mod.sh\n@@ -0,0 +1 @@\n+check\n",
		FixPatch:  "diff --git a/src/mod.txt b/src/mod.txt\n--- a/src/mod.txt\n+++ b/src/mod.txt\n@@ -1 +1 @@\n-broken\n+fixed\n",
	}
}

func request(mode string) issuebench.WorkRequest {
	r := issuebench.WorkRequest{Mode: mode, OrgID: "org1", JobID: "J_AbC123", Repo: "acme/tiny", Version: "2025-03", Candidate: candidate(), Private: true}
	if mode == issuebench.WorkModeBuild {
		r.Provider, r.Model, r.MaxUSD = "openai", "m1", 2
	} else {
		r.Env = &issuebench.EnvSpec{Language: "aworkfake", BaseImage: "img:a", Dockerfile: "FROM img:a\nCOPY . /testbed\nRUN touch .installed\n"}
	}
	return r
}

func secretsAll() map[string]string {
	return map[string]string{
		issuebench.SecretGitHubToken:      fakeToken,
		issuebench.SecretGatewayKey:       fakeVK,
		issuebench.SecretGatewayURL:       "http://shared.invalid",
		issuebench.SecretGatewayPublicURL: "http://public.invalid",
	}
}

// fakeDocker records every docker call and answers test runs from the
// patches in the script: the fix applied passes the test, else it fails.
type fakeDocker struct {
	mu        sync.Mutex
	calls     [][]string
	stdins    []string
	failBuild bool
}

func (f *fakeDocker) run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.stdins = append(f.stdins, string(stdin))
	switch args[0] {
	case "build":
		if f.failBuild {
			return []byte("pip install failed"), &issuebench.ExitError{Code: 1, Output: []byte("pip install failed")}
		}
		return []byte("sha256:abc"), nil
	case "run":
		s := string(stdin)
		if !strings.Contains(s, "/tmp/tests.patch") {
			return []byte("collected 0 items"), &issuebench.ExitError{Code: 5}
		}
		if strings.Contains(s, "/tmp/fix.patch") {
			return []byte("PASS tests/test_mod.sh::test_it\n"), nil
		}
		return []byte("FAIL tests/test_mod.sh::test_it\n"), &issuebench.ExitError{Code: 1}
	}
	return nil, nil
}

func (f *fakeDocker) verbs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c[0])
	}
	return out
}

// localSandbox runs commands with real bash on a host copy of the repo.
type localSandbox struct {
	t    *testing.T
	dir  string
	snap int
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if out, err := exec.Command("cp", "-a", src+"/.", dst).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
}

func (s *localSandbox) Exec(ctx context.Context, cmd string, _ time.Duration) (int, string, error) {
	cmd = strings.ReplaceAll(cmd, builder.Workdir, s.dir)
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Dir = s.dir
	out, err := c.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out), nil
	}
	return 0, string(out), err
}

func (s *localSandbox) Snapshot(context.Context) (string, error) {
	s.snap++
	d := s.dir + fmt.Sprintf(".snap%d", s.snap)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	copyDir(s.t, s.dir, d)
	return d, nil
}

func (s *localSandbox) Rollback(_ context.Context, id string) error {
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	copyDir(s.t, id, s.dir)
	return nil
}

func (s *localSandbox) Close(context.Context) error { return nil }

// fakeGatewayServer answers chat completions with scripted tool calls and
// records the virtual key header of every call.
type fakeGatewayServer struct {
	mu    sync.Mutex
	calls [][]builder.ToolCall
	keys  []string
	srv   *httptest.Server
}

func newGatewayServer(t *testing.T, turns ...[]builder.ToolCall) *fakeGatewayServer {
	g := &fakeGatewayServer{calls: turns}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.keys = append(g.keys, r.Header.Get("x-bf-vk"))
		if r.URL.Path != "/v1/chat/completions" || len(g.calls) == 0 {
			http.Error(w, "no more turns", 500)
			return
		}
		turn := g.calls[0]
		g.calls = g.calls[1:]
		type fn struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		type call struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function fn     `json:"function"`
		}
		var cs []call
		for _, c := range turn {
			cs = append(cs, call{ID: c.ID, Type: "function", Function: fn{Name: c.Name, Arguments: string(c.Args)}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "", "tool_calls": cs}}},
			"usage":   map[string]any{"prompt_tokens": 1000, "completion_tokens": 100},
		})
	}))
	t.Cleanup(g.srv.Close)
	return g
}

type harness struct {
	docker     *fakeDocker
	tokens     []string
	sandboxes  int
	cleanups   int
	logs       []string
	deps       Deps
	scratchDir string
}

func newHarness(t *testing.T) *harness {
	h := &harness{docker: &fakeDocker{}, scratchDir: t.TempDir()}
	src := fixture(t)
	h.deps = Deps{
		Docker: h.docker.run,
		Checkout: func(_ context.Context, repo, sha, dir, token string) error {
			if repo != "acme/tiny" || sha != baseSHA {
				return fmt.Errorf("unexpected checkout %s %s", repo, sha)
			}
			h.tokens = append(h.tokens, token)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			copyDir(t, src, dir)
			return nil
		},
		Sandbox: func(context.Context, issuebench.WorkRequest) (builder.SandboxFactory, func(), error) {
			h.sandboxes++
			f := func(_ context.Context, _, repo string, _ []string) (builder.Sandbox, error) {
				d := t.TempDir()
				copyDir(t, repo, d)
				return &localSandbox{t: t, dir: d}, nil
			}
			return f, func() { h.cleanups++ }, nil
		},
		Price:   func(_ string, tok schemas.Tokens) (float64, bool) { return float64(tok.Fresh+tok.Out) / 100000, true },
		Scratch: h.scratchDir,
		Runtime: "runsc",
		Log:     func(format string, args ...any) { h.logs = append(h.logs, fmt.Sprintf(format, args...)) },
	}
	return h
}

func happyTurns() [][]builder.ToolCall {
	return [][]builder.ToolCall{
		{builder.Call("1", "bash", map[string]any{"cmd": "touch .installed"})},
		{builder.Call("2", "run_tests", map[string]any{"state": "base+tests"})},
		{builder.Call("3", "run_tests", map[string]any{"state": "gold"})},
		{builder.Call("4", "submit", map[string]any{})},
	}
}

// noSecretsIn fails when a secret value reaches docker argv or stdin. The
// test script on stdin carries only the base64 patches.
func noSecretsIn(t *testing.T, h *harness) {
	t.Helper()
	for i, c := range h.docker.calls {
		all := strings.Join(c, " ") + "\n" + h.docker.stdins[i]
		for _, s := range []string{fakeToken, fakeVK} {
			if strings.Contains(all, s) {
				t.Fatalf("secret in docker call %d: %q", i, all)
			}
		}
	}
	for _, l := range h.logs {
		if strings.Contains(l, fakeToken) || strings.Contains(l, fakeVK) {
			t.Fatalf("secret in log %q", l)
		}
	}
}

func TestRunBuildHappyPath(t *testing.T) {
	h := newHarness(t)
	gw := newGatewayServer(t, happyTurns()...)
	sec := secretsAll()
	sec[issuebench.SecretGatewayURL] = gw.srv.URL
	res, err := Run(context.Background(), h.deps, request(issuebench.WorkModeBuild), sec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "build" || res.Build == nil || res.Build.Stop != issuebench.StopOK || res.Build.Env == nil {
		t.Fatalf("result %+v", res)
	}
	if res.CostUSD <= 0 || res.CostUSD != res.Build.CostUSD || res.Stop != "" || res.Seconds <= 0 {
		t.Fatalf("cost %v stop %q seconds %v", res.CostUSD, res.Stop, res.Seconds)
	}
	if res.Validation == nil || strings.Join(res.Validation.FailToPass, ",") != "tests/test_mod.sh::test_it" || res.Validation.Stop != "" {
		t.Fatalf("validation %+v", res.Validation)
	}
	if c := res.Validation.Checks; c.Leak || c.Clear || c.InterfaceNote != "" || len(c.WrongPatches) > 0 {
		t.Fatalf("checks must stay zero: %+v", res.Validation.Checks)
	}
	if h.sandboxes != 1 || h.cleanups != 1 || len(h.tokens) != 1 || h.tokens[0] != fakeToken {
		t.Fatalf("sandboxes %d cleanups %d tokens %v", h.sandboxes, h.cleanups, h.tokens)
	}
	for _, k := range gw.keys {
		if k != fakeVK {
			t.Fatalf("gateway saw key %q", k)
		}
	}
	tag := EnvImageTag(request(issuebench.WorkModeBuild))
	verbs := h.docker.verbs()
	if verbs[0] != "build" || verbs[len(verbs)-1] != "image" {
		t.Fatalf("docker verbs %v", verbs)
	}
	b := h.docker.calls[0]
	want := []string{"build", "--quiet", "-t", tag, "-f", filepath.Join(h.scratchDir, "Dockerfile.env"), filepath.Join(h.scratchDir, "repo")}
	if strings.Join(b, " ") != strings.Join(want, " ") {
		t.Fatalf("build argv %v", b)
	}
	if fi, err := os.Stat(filepath.Join(h.scratchDir, "Dockerfile.env")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("Dockerfile.env: %v", err)
	}
	if df, _ := os.ReadFile(filepath.Join(h.scratchDir, "Dockerfile.env")); string(df) != res.Build.Env.Dockerfile {
		t.Fatalf("Dockerfile.env is not the built env")
	}
	run := strings.Join(h.docker.calls[1], " ")
	for _, w := range []string{"run --rm -i --network none --memory 4g --cpus 2 --pids-limit 2048 --security-opt no-new-privileges -w /testbed",
		"--label heimdall.issuebench=validate --label heimdall.issuebench.job=J_AbC123 --runtime runsc " + tag + " bash -s"} {
		if !strings.Contains(run, w) {
			t.Fatalf("run argv %q lacks %q", run, w)
		}
	}
	last := h.docker.calls[len(h.docker.calls)-1]
	if strings.Join(last, " ") != "image rm -f "+tag {
		t.Fatalf("last call %v", last)
	}
	if _, err := os.Stat(filepath.Join(h.scratchDir, "build.jsonl")); err != nil {
		t.Fatalf("transcript: %v", err)
	}
	noSecretsIn(t, h)
}

func TestRunValidateMode(t *testing.T) {
	h := newHarness(t)
	h.deps.Sandbox = nil // a validate job never starts the builder
	h.deps.Runtime = ""
	req := request(issuebench.WorkModeValidate)
	req.Private = false
	res, err := Run(context.Background(), h.deps, req, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Build != nil || res.CostUSD != 0 || res.Validation == nil || len(res.Validation.FailToPass) != 1 {
		t.Fatalf("result %+v", res)
	}
	if len(h.tokens) != 1 || h.tokens[0] != "" {
		t.Fatalf("a public repository gets no token: %v", h.tokens)
	}
	for _, c := range h.docker.calls {
		if c[0] == "run" && strings.Contains(strings.Join(c, " "), "--runtime") {
			t.Fatalf("no runtime set, yet %v", c)
		}
	}
	if v := h.docker.verbs(); v[len(v)-1] != "image" {
		t.Fatalf("image not removed: %v", v)
	}
}

func TestRunStopSkipsValidation(t *testing.T) {
	h := newHarness(t)
	gw := newGatewayServer(t, []builder.ToolCall{builder.Call("1", "submit", map[string]any{"blocker": "needs_gpu", "detail": "imports torch.cuda"})})
	sec := secretsAll()
	sec[issuebench.SecretGatewayURL] = gw.srv.URL
	res, err := Run(context.Background(), h.deps, request(issuebench.WorkModeBuild), sec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Build == nil || res.Build.Stop != issuebench.StopNeedsGPU || res.Validation != nil || !strings.Contains(res.Stop, "needs_gpu") {
		t.Fatalf("result %+v", res)
	}
	if len(h.docker.calls) != 0 {
		t.Fatalf("docker ran after a stop: %v", h.docker.calls)
	}
}

func TestRunMissingGatewayURL(t *testing.T) {
	h := newHarness(t)
	sec := secretsAll()
	delete(sec, issuebench.SecretGatewayPublicURL)
	h.deps.UsePublicGateway = true
	_, err := Run(context.Background(), h.deps, request(issuebench.WorkModeBuild), sec)
	if err == nil || err.Error() != "this worker can not reach the server's model gateway: the operator sets BENCH_GATEWAY_PUBLIC_URL" {
		t.Fatalf("err %v", err)
	}
	if h.sandboxes != 0 {
		t.Fatal("sandbox started without a gateway")
	}
}

func TestRunPublicGatewayPicked(t *testing.T) {
	h := newHarness(t)
	gw := newGatewayServer(t, []builder.ToolCall{builder.Call("1", "submit", map[string]any{"blocker": "needs_gpu"})})
	sec := secretsAll()
	sec[issuebench.SecretGatewayPublicURL] = gw.srv.URL // the shared URL is unreachable
	h.deps.UsePublicGateway = true
	res, err := Run(context.Background(), h.deps, request(issuebench.WorkModeBuild), sec)
	if err != nil || res.Build == nil || res.Build.Stop != issuebench.StopNeedsGPU {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestRunImageRemovedOnFailure(t *testing.T) {
	h := newHarness(t)
	h.docker.failBuild = true
	_, err := Run(context.Background(), h.deps, request(issuebench.WorkModeValidate), secretsAll())
	if err == nil || !strings.Contains(err.Error(), "the environment image did not build: pip install failed") {
		t.Fatalf("err %v", err)
	}
	v := h.docker.verbs()
	if len(v) != 2 || v[0] != "build" || v[1] != "image" {
		t.Fatalf("docker verbs %v", v)
	}
}

func TestRunBuildKeepsResultWhenImageFails(t *testing.T) {
	h := newHarness(t)
	h.docker.failBuild = true
	gw := newGatewayServer(t, happyTurns()...)
	sec := secretsAll()
	sec[issuebench.SecretGatewayURL] = gw.srv.URL
	res, err := Run(context.Background(), h.deps, request(issuebench.WorkModeBuild), sec)
	if err != nil || res.Build == nil || res.Build.Env == nil || res.CostUSD <= 0 || !strings.Contains(res.Stop, "did not build") || res.Validation != nil {
		t.Fatalf("res %+v err %v", res, err)
	}
}

func TestRunBaseImageRefused(t *testing.T) {
	h := newHarness(t)
	req := request(issuebench.WorkModeValidate)
	req.Env.BaseImage = "evil.example/miner:latest"
	res, err := Run(context.Background(), h.deps, req, secretsAll())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stop, `base image "evil.example/miner:latest" is not a base image Heimdall allows`) || res.Validation != nil {
		t.Fatalf("result %+v", res)
	}
	if len(h.docker.calls) != 0 {
		t.Fatalf("docker ran: %v", h.docker.calls)
	}
}

func TestRunRefusesBadRequestAndMissingToken(t *testing.T) {
	h := newHarness(t)
	req := request(issuebench.WorkModeValidate)
	req.Candidate.BaseSHA = "main"
	if _, err := Run(context.Background(), h.deps, req, nil); err == nil || !strings.Contains(err.Error(), "full commit id") {
		t.Fatalf("err %v", err)
	}
	if _, err := Run(context.Background(), h.deps, request(issuebench.WorkModeValidate), nil); err == nil || !strings.Contains(err.Error(), "no GitHub token") {
		t.Fatalf("err %v", err)
	}
	if len(h.tokens) != 0 {
		t.Fatal("checked out on a bad request")
	}
}

func TestRunRedactsErrors(t *testing.T) {
	h := newHarness(t)
	h.deps.Checkout = func(_ context.Context, _, _, _, token string) error {
		return errors.New("fatal: could not read from https://x-access-token:" + token + "@github.com")
	}
	_, err := Run(context.Background(), h.deps, request(issuebench.WorkModeValidate), secretsAll())
	if err == nil || strings.Contains(err.Error(), fakeToken) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err %v", err)
	}
}

// recordingGateway is a schemas.Gateway that records mints and revokes.
type recordingGateway struct{ minted, revoked int }

func (g *recordingGateway) GetName() string    { return "fake" }
func (g *recordingGateway) GetVersion() string { return "0" }
func (g *recordingGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return nil, nil
}
func (g *recordingGateway) MintKey(context.Context, *schemas.RunContext) (*schemas.RunKey, error) {
	g.minted++
	return &schemas.RunKey{ID: "vk-minted", Value: "sk-minted-here"}, nil
}
func (g *recordingGateway) RevokeKey(context.Context, *schemas.RunKey) error { g.revoked++; return nil }
func (g *recordingGateway) RevokeStale(context.Context, map[string]bool) (int, error) {
	return 0, nil
}
func (g *recordingGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return nil, nil
}

func TestPremintedKeyNotRevoked(t *testing.T) {
	gw := newGatewayServer(t, []builder.ToolCall{builder.Call("1", "bash", map[string]any{"cmd": "ls"})})
	rec := &recordingGateway{}
	m := builder.NewGatewayModel(builder.GatewayConfig{Gateway: rec, Key: &schemas.RunKey{Value: fakeVK}, URL: gw.srv.URL, Provider: "openai", Model: "m1"})
	if _, err := m.Next(context.Background(), []builder.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	m.Close(context.Background())
	if rec.minted != 0 || rec.revoked != 0 || len(gw.keys) != 1 || gw.keys[0] != fakeVK {
		t.Fatalf("minted %d revoked %d keys %v", rec.minted, rec.revoked, gw.keys)
	}
	if _, err := m.Next(context.Background(), nil, nil); err == nil {
		t.Fatal("a closed model must refuse")
	}
}
