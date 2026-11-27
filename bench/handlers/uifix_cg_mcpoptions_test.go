package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type cgSuites struct {
	SuiteSource
	s *schemas.Suite
}

func (c cgSuites) LoadSuite(_ context.Context, id string) (*schemas.Suite, error) {
	if id != c.s.ID {
		return nil, schemas.ErrNotFound
	}
	return c.s, nil
}

// TestMCPOptions: services and poisoning probes of a benchmark the caller
// reads; a benchmark GET /suites/{id} refuses (another org's, hidden) is a
// 404; suite is required.
func TestMCPOptions(t *testing.T) {
	s := &schemas.Suite{ID: "s1",
		Services: []schemas.ServiceSpec{{ID: "crm", Role: "mcp"}, {ID: "db", Role: "data"}, {ID: "secret", Role: "tool", Hidden: true}},
		Tasks:    []*schemas.Task{{ID: "t1", Services: []schemas.ServiceSpec{{ID: "mail", Role: "tool"}}}},
		Probes: []*schemas.ProbeSpec{
			{ID: "poison-desc", Apply: schemas.ProbeApply{Tool: &schemas.ToolFault{Mode: "describe_poison"}}},
			{ID: "broken", Apply: schemas.ProbeApply{Tool: &schemas.ToolFault{Mode: "error"}}},
		}}
	r := router.New()
	r.GET(APIPrefix+"/suites/{id}", func(ctx *fasthttp.RequestCtx) {
		if ctx.UserValue("id") != "s1" {
			ctx.SetStatusCode(404)
			return
		}
		ctx.SetBodyString(`{"id":"s1"}`)
	})
	h := &mcpOptionsHandler{env: OrgEnv{Mode: schemas.ModeLive, Services: Services{Suites: cgSuites{s: s}}}, r: r}
	r.GET(MCPOptionsPath, h.get)
	code, body := abCall(r, "GET", MCPOptionsPath+"?suite=s1", nil)
	var out MCPOptions
	if code != 200 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("%d %s", code, body)
	}
	if len(out.Services) != 2 || out.Services[0] != "crm" || out.Services[1] != "mail" || len(out.PoisonProbes) != 1 || out.PoisonProbes[0].ID != "poison-desc" {
		t.Fatalf("options %+v", out)
	}
	if code, _ := abCall(r, "GET", MCPOptionsPath+"?suite=other_org_suite", nil); code != 404 {
		t.Fatalf("unreadable suite = %d", code)
	}
	if code, _ := abCall(r, "GET", MCPOptionsPath, nil); code != 400 {
		t.Fatalf("no suite = %d", code)
	}
}
