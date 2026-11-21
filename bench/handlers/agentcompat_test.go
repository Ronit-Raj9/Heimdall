package handlers

import (
	"context"
	"encoding/json"
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
	if out.Agent != "codex" || !out.Defaulted || len(out.Capabilities) != 3 || out.Capabilities[0] != "shell" {
		t.Fatalf("agent part = %+v", out)
	}
	if len(out.Benchmarks) != 3 {
		t.Fatalf("benchmarks = %+v", out.Benchmarks)
	}
	tb, mem, tau := out.Benchmarks[0], out.Benchmarks[1], out.Benchmarks[2]
	if !tb.OK || tb.Requires[0] != "shell" || len(tb.Missing) != 0 || tb.Reason != "" {
		t.Fatalf("tb2 = %+v", tb)
	}
	if mem.OK || len(mem.Missing) != 1 || mem.Missing[0] != "sessions" || mem.Reason != "agent codex cannot run benchmark longmemeval: needs sessions" {
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
