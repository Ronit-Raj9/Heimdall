package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fasthttp/router"
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
