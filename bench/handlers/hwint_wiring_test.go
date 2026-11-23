package handlers

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/valyala/fasthttp"
)

// Fix wave 3 final integration (N216): create in handlers/evaluations.go
// (lane H13) calls the real platform refusal hook (lane H05), so an
// evaluation whose runs go to a platform that cannot run its benchmarks is
// answered 422 not_on_platform with the benchmark list, and nothing is saved.
func TestCreateRefusesOnPlatformWithBenchmarkList(t *testing.T) {
	withPlatformTarget(t, false) // the shared queue goes to Modal, no image registry saved
	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org_a"})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", `{`+oneArm+`,"benchmarks":["longmemeval"]}`)
	if ctx.Response.StatusCode() != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("got %d %s, want 422", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var b hw05Body
	if err := json.Unmarshal(ctx.Response.Body(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Error.Type != "not_on_platform" {
		t.Fatalf("error type %q, want not_on_platform (%s)", b.Error.Type, ctx.Response.Body())
	}
	if len(b.Benchmarks) != 1 || b.Benchmarks[0].ID != "longmemeval" || len(b.Benchmarks[0].Reasons) == 0 {
		t.Fatalf("benchmarks %+v, want longmemeval with its reason", b.Benchmarks)
	}
	if evs, _ := evalstore.Open(dir).List(); len(evs) != 0 || len(l.planned) != 0 {
		t.Fatalf("saved %d evaluations, planned %v", len(evs), l.planned)
	}
}
