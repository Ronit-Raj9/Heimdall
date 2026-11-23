package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func hw10RunDetailAllowed(t *testing.T, h *RunsHandler) map[string]any {
	t.Helper()
	r := router.New()
	h.RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(APIPrefix + "/runs/r_1")
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 {
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

// N212: a server that cannot hold model calls (the hosted server) says so
// on the run detail, so the page never offers Debug with breakpoints.
func TestRunDetailBreakpointsFalseWithoutHolds(t *testing.T) {
	a := hw10RunDetailAllowed(t, NewRunsHandler(testMode, debugIndex(true, schemas.ConditionClean, "")))
	if a["fork"] != true {
		t.Fatalf("a clean run allows Fork: %v", a)
	}
	if a["breakpoints"] != false {
		t.Errorf("breakpoints = %v, want false without a hold controller", a["breakpoints"])
	}
	if r, _ := a["breakpoints_reason"].(string); !strings.Contains(r, "Pausing before model calls needs a worker this server controls") {
		t.Errorf("breakpoints_reason = %q", r)
	}
}

func TestRunDetailBreakpointsWithHolds(t *testing.T) {
	a := hw10RunDetailAllowed(t, NewRunsHandler(testMode, debugIndex(true, schemas.ConditionClean, "")).WithBreakpoints(true))
	if a["breakpoints"] != true || a["breakpoints_reason"] != nil {
		t.Errorf("with holds: %v, want breakpoints true and no reason", a)
	}
	// A run that cannot be forked cannot be debugged either.
	a = hw10RunDetailAllowed(t, NewRunsHandler(testMode, debugIndex(true, schemas.GenericProbeBrokenTool, "")).WithBreakpoints(true))
	if a["breakpoints"] != false {
		t.Errorf("fault run: %v, want breakpoints false", a)
	}
}
