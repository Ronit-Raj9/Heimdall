package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// bpIndex holds one sealed clean run, enough for GET /runs/{id} to answer
// debug_allowed.
type bpIndex struct{}

func (bpIndex) Experiments() []string                     { return nil }
func (bpIndex) Manifest(string) (*schemas.Manifest, bool) { return nil, false }
func (bpIndex) Runs(string) []handlers.RunView            { return nil }
func (bpIndex) Pair(string) []handlers.RunView            { return nil }
func (bpIndex) RunFile(string, string) ([]byte, error)    { return nil, os.ErrNotExist }
func (bpIndex) Run(id string) (*handlers.RunView, bool) {
	if id != "r_bp" {
		return nil, false
	}
	return &handlers.RunView{Sealed: true,
		Slot: schemas.Slot{ExpID: "e_bp", Arm: "A", Task: "t01", Condition: schemas.ConditionClean},
		Run:  schemas.Run{RunID: "r_bp", ExpID: "e_bp"}}, true
}

// bpHolds is a hold controller: a server that runs its experiments behind
// its own fault proxy has one.
type bpHolds struct{}

func (bpHolds) Holds(context.Context, string) (*schemas.HoldStatus, error) { return nil, nil }
func (bpHolds) ReleaseHold(context.Context, string, schemas.HoldRelease) error {
	return nil
}

func bpDebugAllowed(t *testing.T, h fasthttp.RequestHandler) map[string]any {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(handlers.APIPrefix + "/runs/r_bp")
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.Header.SetHost("127.0.0.1")
	h(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("GET run: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var got struct {
		DebugAllowed map[string]any `json:"debug_allowed"`
	}
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil || got.DebugAllowed == nil {
		t.Fatalf("debug_allowed missing: %v %s", err, ctx.Response.Body())
	}
	return got.DebugAllowed
}

// Fix wave 3, N212: the runs handler is told whether this server can hold
// model calls. A local server with a hold controller offers Debug with
// breakpoints; a server without one (the hosted org routers) says why not.
func TestRunsHandlerBreakpointsFollowHolds(t *testing.T) {
	s, err := New(testConfig(), Deps{Index: bpIndex{}, Services: handlers.Services{Holds: bpHolds{}}})
	if err != nil {
		t.Fatal(err)
	}
	a := bpDebugAllowed(t, s.handler)
	if a["breakpoints"] != true {
		t.Errorf("local server with holds: breakpoints = %v, want true (%v)", a["breakpoints"], a)
	}
	if r, _ := a["breakpoints_reason"].(string); r != "" {
		t.Errorf("local server with holds: breakpoints_reason = %q, want none", r)
	}

	// A hosted org router is built by NewOrgRouter over deps without holds.
	org := NewOrgRouter(testConfig(), "o_bp", Deps{Index: bpIndex{}})
	found := false
	for _, routes := range org.Routes {
		for _, p := range routes {
			found = found || strings.HasSuffix(p, "/runs/{id}")
		}
	}
	if !found {
		t.Fatal("org router has no GET /runs/{id}")
	}
	s2, err := New(testConfig(), Deps{Index: bpIndex{}})
	if err != nil {
		t.Fatal(err)
	}
	a = bpDebugAllowed(t, s2.handler)
	if a["breakpoints"] != false {
		t.Errorf("server without holds: breakpoints = %v, want false", a["breakpoints"])
	}
	if r, _ := a["breakpoints_reason"].(string); !strings.Contains(r, "Pausing before model calls needs a worker this server controls") {
		t.Errorf("server without holds: breakpoints_reason = %q", r)
	}
}
