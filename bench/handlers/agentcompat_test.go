package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type compatAgents []AgentConfigFile

func (c compatAgents) AgentConfigs(context.Context) ([]AgentConfigFile, error) { return c, nil }

func doCompat(t *testing.T, env OrgEnv, id string) (int, AgentCompatibility) {
	t.Helper()
	r := router.New()
	registerAgentCompat(env, r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, nil, nil)
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(APIPrefix + "/agents/" + id + "/compatibility")
	r.Handler(ctx)
	var out AgentCompatibility
	_ = json.Unmarshal(ctx.Response.Body(), &out)
	return ctx.Response.StatusCode(), out
}

func TestAgentCompatibility(t *testing.T) {
	suites := &fakeSuites{
		ids: []string{"hd-harbor-tb2", "longmemeval", "tau2"},
		suites: map[string]*schemas.Suite{
			"hd-harbor-tb2": {ID: "hd-harbor-tb2", Track: "terminal"},
			"longmemeval":   {ID: "longmemeval", Track: "memory"},
			"tau2":          {ID: "tau2", Track: "support"},
		},
	}
	agents := compatAgents{{Path: "agents/codex.yaml", Config: &schemas.AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1"}}}
	env := OrgEnv{Mode: "local", Services: Services{Suites: suites, Agents: agents}}
	code, out := doCompat(t, env, "codex")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if out.Agent != "codex" || !out.Defaulted || len(out.Capabilities) != 4 || out.Capabilities[0] != "shell" {
		t.Fatalf("agent part = %+v", out)
	}
	if len(out.Benchmarks) != 3 {
		t.Fatalf("benchmarks = %+v", out.Benchmarks)
	}
	tb, mem, tau := out.Benchmarks[0], out.Benchmarks[1], out.Benchmarks[2]
	if !tb.OK || tb.Requires[0] != "shell" || len(tb.Missing) != 0 || tb.Reason != "" {
		t.Fatalf("tb2 = %+v", tb)
	}
	// CLI coding agents default to sessions: memory benchmarks run them
	// across sessions through the notes and memory folder.
	if !mem.OK || len(mem.Missing) != 0 || mem.Reason != "" {
		t.Fatalf("longmemeval = %+v", mem)
	}
	if !tau.OK {
		t.Fatalf("tau2 = %+v", tau)
	}
	if code, _ := doCompat(t, env, "nobody"); code != 404 {
		t.Fatalf("unknown agent status %d", code)
	}
	if code, _ := doCompat(t, OrgEnv{Mode: "local", Services: Services{Agents: agents}}, "codex"); code != 503 {
		t.Fatalf("no suites status %d", code)
	}
}

// headSuites serves heads only, with a stamp per pack, and counts reads.
type headSuites struct {
	*fakeSuites
	dir          string
	mu           sync.Mutex
	stamp        string
	heads, loads atomic.Int32
}

func (s *headSuites) SuiteStamp(id string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir + "/" + id, s.stamp
}

func (s *headSuites) SuiteHead(id string) (*schemas.Suite, error) {
	s.heads.Add(1)
	full := s.fakeSuites.suites[id]
	return &schemas.Suite{ID: full.ID, Track: full.Track, Environment: full.Environment, Requires: full.Requires}, nil
}

func (s *headSuites) LoadSuite(ctx context.Context, id string) (*schemas.Suite, error) {
	s.loads.Add(1)
	return s.fakeSuites.LoadSuite(ctx, id)
}

// TestAgentCompatibilityReadsHeadsAndCaches: the check never loads a full
// pack when heads are available, answers again from the cache, and reads
// again when a pack's stamp or the agent config changes.
func TestAgentCompatibilityReadsHeadsAndCaches(t *testing.T) {
	src := &headSuites{dir: t.TempDir(), stamp: "s1", fakeSuites: &fakeSuites{
		ids: []string{"desk", "tb2"},
		suites: map[string]*schemas.Suite{
			"desk": {ID: "desk", Track: schemas.TrackComputerUse},
			"tb2":  {ID: "tb2", Track: "terminal"},
		},
	}}
	cfg := &schemas.AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1"}
	agents := compatAgents{{Path: "agents/codex.yaml", Config: cfg}}
	env := OrgEnv{Mode: "local", Services: Services{Suites: src, Agents: agents}}
	r := router.New()
	registerAgentCompat(env, r)
	call := func() AgentCompatibility {
		ctx := &fasthttp.RequestCtx{}
		ctx.Init(&fasthttp.Request{}, nil, nil)
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.SetRequestURI(APIPrefix + "/agents/codex/compatibility")
		r.Handler(ctx)
		if ctx.Response.StatusCode() != 200 {
			t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
		var out AgentCompatibility
		_ = json.Unmarshal(ctx.Response.Body(), &out)
		return out
	}
	out := call()
	if len(out.Benchmarks) != 2 || out.Benchmarks[0].OK || out.Benchmarks[0].Missing[0] != "desktop" || !out.Benchmarks[1].OK {
		t.Fatalf("answer = %+v", out.Benchmarks)
	}
	if src.loads.Load() != 0 || src.heads.Load() != 2 {
		t.Fatalf("loads %d heads %d, want heads only", src.loads.Load(), src.heads.Load())
	}
	call()
	if src.heads.Load() != 2 {
		t.Fatalf("heads after a cached answer = %d, want 2", src.heads.Load())
	}
	src.mu.Lock()
	src.stamp = "s2"
	src.mu.Unlock()
	call()
	if src.heads.Load() != 4 {
		t.Fatalf("heads after a stamp change = %d, want 4", src.heads.Load())
	}
	cfg.Capabilities = schemas.CapabilitySet{schemas.CapShell, schemas.CapDesktop}
	if out := call(); !out.Benchmarks[0].OK {
		t.Fatalf("after the agent gained desktop = %+v", out.Benchmarks[0])
	}
	if src.loads.Load() != 0 {
		t.Fatalf("full loads = %d, want 0", src.loads.Load())
	}
}

// TestCompatibilityTaskImageAgentNotRunnable: a benchmark that grades a
// snapshot of the agent's container on tasks with their own image needs an
// agent built on each task's image. A container agent whose Dockerfile does
// not start FROM ${BASE_IMAGE} cannot join it, so the compatibility answer
// says the benchmark is not runnable, with the same check the estimate runs.
func TestCompatibilityTaskImageAgentNotRunnable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents/images/claude-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents/images/claude-code/Dockerfile"), []byte("FROM node:22\nRUN true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents/images/codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents/images/codex/Dockerfile"), []byte("ARG BASE_IMAGE=ubuntu:24.04\nFROM ${BASE_IMAGE}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	harbor := &schemas.Suite{ID: "hd-harbor-bfcl", Track: "terminal", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot},
		Tasks: []*schemas.Task{{ID: "t1", Environment: &schemas.TaskEnvironment{Image: "harbor/bfcl-t1:1"}}}}
	plain := &schemas.Suite{ID: "tb-plain", Track: "terminal"}
	suites := &fakeSuites{ids: []string{"hd-harbor-bfcl", "tb-plain"}, suites: map[string]*schemas.Suite{"hd-harbor-bfcl": harbor, "tb-plain": plain}}
	agents := compatAgents{
		{Path: "agents/claude-code.yaml", Config: &schemas.AgentConfig{ID: "claude-code", Adapter: "command", Image: "bench/agent-claude-code:1",
			Build: &schemas.ImageSpec{Dockerfile: "agents/images/claude-code/Dockerfile", Tag: "bench/agent-claude-code:1"}}},
		{Path: "agents/codex.yaml", Config: &schemas.AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1",
			Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex:1"}}},
	}
	env := OrgEnv{Mode: "local", Cfg: &lib.Config{Root: root}, Services: Services{Suites: suites, Agents: agents}}
	code, out := doCompat(t, env, "claude-code")
	if code != 200 || len(out.Benchmarks) != 2 {
		t.Fatalf("status %d answer %+v", code, out)
	}
	bfcl, other := out.Benchmarks[0], out.Benchmarks[1]
	want := "Runs inside each task's own environment, and claude-code cannot join it (its image is not built on the benchmark's base image)."
	if bfcl.OK || !strings.HasPrefix(bfcl.Reason, want) {
		t.Fatalf("bfcl = %+v, want not runnable with %q", bfcl, want)
	}
	if !strings.Contains(bfcl.Reason, " Details: ") || !strings.Contains(bfcl.Reason, "does not start FROM ${BASE_IMAGE}") {
		t.Fatalf("bfcl reason %q: want the technical text after Details", bfcl.Reason)
	}
	if !other.OK {
		t.Fatalf("plain benchmark = %+v, want runnable", other)
	}
	if code, out := doCompat(t, env, "codex"); code != 200 || !out.Benchmarks[0].OK {
		t.Fatalf("codex (built FROM ${BASE_IMAGE}) = %d %+v, want runnable", code, out.Benchmarks)
	}
}
