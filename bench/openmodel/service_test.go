package openmodel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
)

type fakeCreds struct{ connected bool }

func (f fakeCreds) CredentialsOf(_ context.Context, _, name string) (sandbox.Credentials, error) {
	if !f.connected || name != sandbox.PlatformModal {
		return sandbox.Credentials{}, sandbox.ErrNotConnected
	}
	return sandbox.Credentials{Token: "ak-test:as-test"}, nil
}

// fakeDeployer serves an OpenAI-compatible /v1/models behind the
// deployment's key, like vLLM with --api-key.
type fakeDeployer struct {
	mu       sync.Mutex
	srv      *httptest.Server
	key      string
	spec     DeploySpec
	stopped  []string
	failWith error
	stopErr  error
	apps     map[string]bool
}

func (f *fakeDeployer) Deploy(_ context.Context, _ sandbox.Credentials, s DeploySpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return "", f.failWith
	}
	f.key, f.spec = s.APIKey, s
	if f.apps == nil {
		f.apps = map[string]bool{}
	}
	f.apps[s.AppName] = true
	return f.srv.URL, nil
}

func (f *fakeDeployer) Stop(_ context.Context, _ sandbox.Credentials, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, name)
	delete(f.apps, name)
	return nil
}

func (f *fakeDeployer) List(context.Context, sandbox.Credentials) ([]AppInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []AppInfo{}
	for n := range f.apps {
		out = append(out, AppInfo{Name: n, State: "deployed"})
	}
	return out, nil
}

func newFake(t *testing.T) *fakeDeployer {
	f := &fakeDeployer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		key := f.key
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"Qwen/Qwen2.5-0.5B-Instruct","object":"model"}]}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func newService(t *testing.T, f *fakeDeployer, st Store) (*Service, keystore.Store) {
	keys := keystore.NewMemory()
	return &Service{Keys: keys, Platforms: fakeCreds{connected: true}, Deployer: f, Store: st, PollEvery: 10 * time.Millisecond,
		ReadyWithin: 5 * time.Second}, keys
}

func TestPlanPicksRecommendedGPU(t *testing.T) {
	m, engine, g, hours, err := plan(StartRequest{Model: "qwen/qwen2.5-0.5b-instruct"})
	if err != nil || engine != EngineVLLM || g.Name != "L4" || g.Count != 1 || hours != DefaultMaxHours || m.ID != "Qwen/Qwen2.5-0.5B-Instruct" {
		t.Fatalf("plan = %v %v %+v %v %v", m.ID, engine, g, hours, err)
	}
	if _, _, g, _, err := plan(StartRequest{Model: "Qwen/Qwen2.5-7B-Instruct"}); err != nil || g.Name != "L4" {
		t.Fatalf("7B = %+v %v", g, err)
	}
	if _, _, g, _, err := plan(StartRequest{Model: "meta-llama/Llama-3.3-70B-Instruct"}); err != nil || g.Count < 2 {
		t.Fatalf("70B = %+v %v", g, err)
	}
	if _, _, _, _, err := plan(StartRequest{Model: "Qwen/Qwen2.5-7B-Instruct", GPU: "T4"}); err == nil || !strings.Contains(err.Error(), "bf16") {
		t.Fatalf("7B on T4: %v", err)
	}
	if _, _, g, _, err := plan(StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct", GPU: "a10g", Engine: "sgl"}); err != nil || g.Name != "A10" {
		t.Fatalf("A10G alias: %+v %v", g, err)
	}
	for _, bad := range []StartRequest{{Model: "gpt-4o"}, {Model: "Qwen/Qwen2.5-0.5B-Instruct", Engine: "tgi"},
		{Model: "Qwen/Qwen2.5-0.5B-Instruct", GPU: "V100"}, {Model: "Qwen/Qwen2.5-0.5B-Instruct", MaxHours: 48}} {
		if _, _, _, _, err := plan(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestServerArgs(t *testing.T) {
	m, _ := LookupModel("Qwen/Qwen2.5-0.5B-Instruct")
	t4, _ := sandbox.LookupModalGPU("T4")
	if _, _, g, _, err := plan(StartRequest{Model: m.ID, GPU: "T4"}); err != nil || g.Name != "T4" {
		t.Fatalf("T4 by choice: %+v %v", g, err)
	}
	a := strings.Join(serverArgs(EngineVLLM, m, t4, 1), " ")
	for _, want := range []string{"--max-model-len 8192", "--enable-auto-tool-choice --tool-call-parser hermes", "--dtype half"} {
		if !strings.Contains(a, want) {
			t.Errorf("vllm args %q lack %q", a, want)
		}
	}
	h100, _ := sandbox.LookupModalGPU("H100")
	r1, _ := LookupModel("deepseek-ai/DeepSeek-R1-Distill-Qwen-7B")
	a = strings.Join(serverArgs(EngineSGLang, r1, h100, 2), " ")
	if !strings.Contains(a, "--tp 2") || !strings.Contains(a, "--reasoning-parser deepseek_r1") || strings.Contains(a, "tool-call") || strings.Contains(a, "half") {
		t.Errorf("sglang args %q", a)
	}
}

func waitState(t *testing.T, s *Service, org, id, want string) *Deployment {
	t.Helper()
	s.Wait()
	d, err := s.Get(context.Background(), org, id)
	if err != nil || d.State != want {
		t.Fatalf("deployment = %+v, %v; want state %s", d, err, want)
	}
	return d
}

func TestStartRegistersKeyAndEvaluationEndStopsIt(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	s, keys := newService(t, f, NewMemoryStore())
	ended := false
	s.Ended = func(_ context.Context, org, exp string) (bool, error) { return ended && exp == "exp-1", nil }
	d, err := s.Start(ctx, "org1", "user:ada", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct", ExpID: "exp-1", MaxHours: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateStarting || !appNameRe.MatchString(d.AppName) || d.MaxUSD != 0.8 {
		t.Fatalf("started = %+v", d)
	}
	d = waitState(t, s, "org1", d.ID, StateReady)
	if d.BaseURL != f.srv.URL+"/v1" || d.KeyID == "" || d.ModelName != "vllm/Qwen/Qwen2.5-0.5B-Instruct" {
		t.Fatalf("ready = %+v", d)
	}
	k, err := keys.Get(ctx, "org1", d.KeyID)
	if err != nil || k.Provider != "vllm" || k.BaseURL != d.BaseURL || len(k.Models) != 1 || k.Status != "ok" {
		t.Fatalf("key = %+v %v", k, err)
	}
	if v, _ := keys.Reveal(ctx, "org1", d.KeyID); v != f.key || !strings.HasPrefix(v, "hmo_") || strings.HasSuffix(v, d.KeyLast4) == false {
		t.Fatal("the registered key is not the deployment's own key")
	}
	if f.spec.ScaledownS != ScaledownSeconds || f.spec.GPU.Name != "L4" {
		t.Fatalf("spec = %+v", f.spec)
	}
	if n := s.Reap(ctx); n != 0 {
		t.Fatalf("reaped %d while the evaluation runs", n)
	}
	ended = true
	if n := s.Reap(ctx); n != 1 {
		t.Fatalf("reaped %d after the evaluation ended", n)
	}
	d = waitState(t, s, "org1", d.ID, StateStopped)
	if !strings.Contains(d.StopWhy, "exp-1") || len(f.stopped) != 1 || f.stopped[0] != d.AppName || d.KeyID != "" {
		t.Fatalf("stopped = %+v, stops %v", d, f.stopped)
	}
	if _, err := keys.Get(ctx, "org1", k.ID); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("key left after stop: %v", err)
	}
	if apps, _ := s.OrphanApps(ctx, "org1"); len(apps) != 0 {
		t.Fatalf("apps left: %v", apps)
	}
}

func TestTimeLimitAndFailedStopRetry(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s, _ := newService(t, f, NewMemoryStore())
	s.Now = func() time.Time { return now }
	d, err := s.Start(ctx, "org1", "user:ada", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct", MaxHours: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, s, "org1", d.ID, StateReady)
	now = now.Add(31 * time.Minute)
	f.stopErr = errors.New("modal down")
	s.Reap(ctx)
	waitState(t, s, "org1", d.ID, StateStopping)
	f.stopErr = nil
	if n := s.Reap(ctx); n != 1 {
		t.Fatalf("retry reaped %d", n)
	}
	d = waitState(t, s, "org1", d.ID, StateStopped)
	if d.StopWhy != "time limit reached" {
		t.Fatalf("why = %q", d.StopWhy)
	}
}

func TestStartFailureStopsTheApp(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	f.failWith = errors.New("Modal deploy: image build failed")
	s, keys := newService(t, f, NewMemoryStore())
	d, err := s.Start(ctx, "org1", "user:ada", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct"})
	if err != nil {
		t.Fatal(err)
	}
	d = waitState(t, s, "org1", d.ID, StateFailed)
	if !strings.Contains(d.Detail, "image build failed") || len(f.stopped) != 1 {
		t.Fatalf("failed = %+v, stops %v", d, f.stopped)
	}
	if ks, _ := keys.List(ctx, "org1", keystore.KindProvider); len(ks) != 0 {
		t.Fatalf("keys after a failed start: %v", ks)
	}
}

func TestStopByHandWhileStarting(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	block := make(chan struct{})
	slow := &slowDeployer{fakeDeployer: f, block: block}
	s, keys := newService(t, f, NewMemoryStore())
	s.Deployer = slow
	d, err := s.Start(ctx, "org1", "user:ada", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stop(ctx, "org1", d.ID, "user:ada"); err != nil {
		t.Fatal(err)
	}
	close(block)
	d = waitState(t, s, "org1", d.ID, StateStopped)
	if ks, _ := keys.List(ctx, "org1", keystore.KindProvider); len(ks) != 0 {
		t.Fatalf("a stopped start registered a key: %v", ks)
	}
	if apps, _ := f.List(ctx, sandbox.Credentials{}); len(apps) != 0 {
		t.Fatalf("app left running: %v", apps)
	}
}

type slowDeployer struct {
	*fakeDeployer
	block chan struct{}
}

func (s *slowDeployer) Deploy(ctx context.Context, c sandbox.Credentials, spec DeploySpec) (string, error) {
	<-s.block
	return s.fakeDeployer.Deploy(ctx, c, spec)
}

func TestStartRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	s, _ := newService(t, f, NewMemoryStore())
	s.Platforms = fakeCreds{}
	if _, err := s.Start(ctx, "org1", "u", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct"}); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("not connected: %v", err)
	}
	s.Platforms = fakeCreds{connected: true}
	if _, err := s.Start(ctx, "org1", "u", StartRequest{Model: "meta-llama/Llama-3.1-8B-Instruct"}); !errors.Is(err, ErrNeedsHFSecret) {
		t.Fatalf("gated: %v", err)
	}
	for i := 0; i < MaxActivePerOrg; i++ {
		if _, err := s.Start(ctx, "org1", "u", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Start(ctx, "org1", "u", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("too many: %v", err)
	}
	s.Wait()
	if _, err := s.Get(ctx, "org2", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-org get")
	}
}

func TestDBStore(t *testing.T) {
	ctx := context.Background()
	st := NewDBStore(dbtest.Open(t))
	at := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	a := &Deployment{ID: "aaaaaaaaaa", OrgID: "o1", State: StateReady, CreatedAt: at}
	b := &Deployment{ID: "bbbbbbbbbb", OrgID: "o1", State: StateStopped, CreatedAt: at.Add(time.Minute)}
	c := &Deployment{ID: "cccccccccc", OrgID: "o2", State: StateStarting, CreatedAt: at}
	for _, d := range []*Deployment{a, b, c} {
		if err := st.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	a.Detail = "updated"
	if err := st.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, "o1", a.ID)
	if err != nil || got.Detail != "updated" {
		t.Fatalf("get = %+v %v", got, err)
	}
	if _, err := st.Get(ctx, "o2", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-org get")
	}
	l, _ := st.List(ctx, "o1")
	if len(l) != 2 || l[0].ID != b.ID {
		t.Fatalf("list = %+v", l)
	}
	act, _ := st.Active(ctx)
	if len(act) != 2 {
		t.Fatalf("active = %+v", act)
	}
}

// TestHelperRendersValidApp: modal_serve.py compiles, and the app source it
// writes for a deployment is valid Python.
func TestHelperRendersValidApp(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	script := serveHelper + `
import ast
cfg = {"app_name": "heimdall-om-abcdefghij", "engine": "vllm", "model": "Qwen/Qwen2.5-0.5B-Instruct",
       "served_name": "Qwen/Qwen2.5-0.5B-Instruct", "gpu": "L4", "args": ["--max-model-len", "8192"],
       "vllm_version": "0.33.0", "sglang_version": "0.5.23", "scaledown_s": 300, "startup_timeout_s": 1200,
       "concurrency": 32, "hf_token": False}
src = APP_SOURCE % {"cfg": json.dumps(cfg)}
ast.parse(src)
assert "heimdall-om-abcdefghij" in src
assert "nvidia/cuda:" in src and "-devel-" in src  # the engines JIT kernels with nvcc
assert "os._exit(1)" in src                       # a dead server ends the GPU container
print("ok")
`
	out, err := exec.Command(py, "-I", "-c", strings.Replace(script, `if __name__ == "__main__":
    main()`, "", 1)).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("render: %v %s", err, out)
	}
}

func TestModalDeployerRefusesForeignApps(t *testing.T) {
	if err := (ModalDeployer{Python: "/bin/false"}).Stop(context.Background(), sandbox.Credentials{Token: "a:b"}, "heimdall-sandboxes"); err == nil {
		t.Fatal("stopped an app this package never deployed")
	}
}
