package handlers

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

// K5 (PQ1-18): the fork dialog's body without a cap is refused with a plain
// sentence; with a cap it is accepted and the cap replaces the parent's
// budget in the planned child, where the scheduler stops the run at it.
func TestForkWebBodyNeedsCap(t *testing.T) {
	l := &fakeLauncher{}
	r := debugServer(t, Services{Index: debugIndex(true, "clean", ""), Launcher: l})
	ctx := dbgDo(r, "POST", "/runs/r_1/fork", `{"call":1,"message":"x"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest || !strings.Contains(string(ctx.Response.Body()), "Set a spend cap") {
		t.Fatalf("fork without cap: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if l.planned != nil {
		t.Fatalf("a refused fork planned %+v", l.planned)
	}
	for _, bad := range []string{`{"call":1,"budget_usd":500}`, `{"call":1,"budget_usd":-1}`} {
		if ctx := dbgDo(r, "POST", "/runs/r_1/fork", bad); ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
			t.Fatalf("fork %s: %d", bad, ctx.Response.StatusCode())
		}
	}
	ctx = dbgDo(r, "POST", "/runs/r_1/fork", `{"call":1,"message":"x","budget_usd":0.25}`)
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("fork with cap: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if b := l.planned.Budget; b.TotalUSD != 0.25 || b.ReserveUSD != 0 {
		t.Fatalf("fork budget = %+v, want the 0.25 cap", b)
	}
	if ctx := dbgDo(r, "POST", "/runs/r_1/replay", `{"budget_usd":1}`); ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("replay with a budget: %d", ctx.Response.StatusCode())
	}
}
