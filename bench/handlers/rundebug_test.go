package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

const dbgReq = `{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`

func dbgRaw() string {
	d, _ := json.Marshal(map[string]any{"raw_request": dbgReq, "raw_response": `{"id":"msg_1","content":[{"type":"text","text":"hello"}]}`, "model": "claude-x", "status": "success"})
	b, _ := json.Marshal(map[string]any{"request_id": "req_1", "raw_available": true, "raw_detail": json.RawMessage(d)})
	return string(b) + "\n"
}

func dbgLedger(extra string) string {
	e := schemas.LedgerEntry{RunID: "r_1", Kind: "message", Call: 1, Action: "pass", Status: 200, ReqSHA256: utils.SHA256Bytes([]byte(dbgReq)), Replay: extra}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

type fakeHolds struct {
	status   schemas.HoldStatus
	released []schemas.HoldRelease
}

func (f *fakeHolds) Holds(_ context.Context, runID string) (*schemas.HoldStatus, error) {
	st := f.status
	st.RunID = runID
	return &st, nil
}

func (f *fakeHolds) ReleaseHold(_ context.Context, slot string, r schemas.HoldRelease) error {
	f.released = append(f.released, r)
	return nil
}

func debugServer(t *testing.T, s Services) *router.Router {
	t.Helper()
	r := router.New()
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: schemas.ModeRecorded, Services: s}
	for _, a := range OrgAreas() {
		a(env, r)
	}
	return r
}

func debugIndex(sealed bool, cond, replayLabel string) *fakeIndex {
	m := testManifest()
	m.Experiment.Arms = map[string]schemas.ArmSpec{"A": {Agent: "agents/a.yaml"}, "B": {Agent: "agents/b.yaml"}}
	m.Experiment.Suite = "coding-core@1.0.0"
	return &fakeIndex{
		manifests: map[string]*schemas.Manifest{testExp: m},
		runs: []RunView{{Sealed: sealed, Slot: schemas.Slot{ExpID: testExp, Arm: "B", Task: "t01", Condition: cond},
			Run: schemas.Run{RunID: "r_1", ExpID: testExp}}},
		files: map[string]map[string][]byte{"r_1": {"gateway.raw.jsonl": []byte(dbgRaw()), "proxy.ledger.jsonl": []byte(dbgLedger(replayLabel))}},
	}
}

func dbgDo(r *router.Router, method, path, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(APIPrefix + path)
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.SetBodyString(body)
	r.Handler(ctx)
	return ctx
}

func TestRunCallsAndDetail(t *testing.T) {
	r := debugServer(t, Services{Index: debugIndex(true, "clean", schemas.ReplayDivergence)})
	ctx := dbgDo(r, "GET", "/runs/r_1/calls", "")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("calls %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var calls RunCalls
	_ = json.Unmarshal(ctx.Response.Body(), &calls)
	if len(calls.Calls) != 1 || calls.Calls[0].RequestID != "req_1" || calls.First == nil || calls.First.Call != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	ctx = dbgDo(r, "GET", "/runs/r_1/calls/req_1", "")
	var d RunCallDetail
	_ = json.Unmarshal(ctx.Response.Body(), &d)
	if d.Call != 1 || !strings.Contains(d.Request, `"content": "hi"`) || !strings.Contains(d.Response, "hello") || !d.Available {
		t.Fatalf("detail = %+v", d)
	}
	if ctx := dbgDo(r, "GET", "/runs/r_1/calls/req_x", ""); ctx.Response.StatusCode() != 404 {
		t.Fatalf("unknown call %d", ctx.Response.StatusCode())
	}
}

func TestReplayAndForkStartUncountedChild(t *testing.T) {
	l := &fakeLauncher{}
	r := debugServer(t, Services{Index: debugIndex(true, "clean", ""), Launcher: l})
	ctx := dbgDo(r, "POST", "/runs/r_1/replay", "")
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("replay %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	exp := l.planned
	if exp.Debug == nil || exp.Debug.Mode != schemas.DebugReplay || exp.Debug.FromRun != "r_1" || exp.Debug.Cassette == nil ||
		len(exp.Debug.Cassette.Entries) != 1 || len(exp.Arms) != 1 || exp.Arms["B"].Agent != "agents/b.yaml" ||
		exp.Conditions[0].Tasks.IDs[0] != "t01" || exp.Concurrency != 1 {
		t.Fatalf("replay experiment = %+v", exp)
	}
	ctx = dbgDo(r, "POST", "/runs/r_1/fork", `{"call":1,"message":"check the tests first","budget_usd":0.5}`)
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("fork %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if d := l.planned.Debug; d.Mode != schemas.DebugFork || d.Call != 1 || d.Edit == nil || d.Edit.Message != "check the tests first" {
		t.Fatalf("fork spec = %+v", d)
	}
	for _, body := range []string{`{"call":9,"budget_usd":0.5}`, `{"call":1,"response":"a","message":"b","budget_usd":0.5}`, `{"nope":1}`} {
		if ctx := dbgDo(r, "POST", "/runs/r_1/fork", body); ctx.Response.StatusCode() != 400 {
			t.Fatalf("fork %s: %d", body, ctx.Response.StatusCode())
		}
	}
	if ctx := dbgDo(r, "POST", "/runs/r_1/debug", `{"breakpoints":{"every":true},"budget_usd":0.5}`); ctx.Response.StatusCode() != 409 {
		t.Fatalf("breakpoints without a hold controller: %d", ctx.Response.StatusCode())
	}
}

func TestDebugRefusals(t *testing.T) {
	l := &fakeLauncher{}
	r := debugServer(t, Services{Index: debugIndex(false, "clean", ""), Launcher: l})
	if ctx := dbgDo(r, "POST", "/runs/r_1/replay", ""); ctx.Response.StatusCode() != 409 {
		t.Fatalf("unsealed replay: %d", ctx.Response.StatusCode())
	}
	r = debugServer(t, Services{Index: debugIndex(true, "p_outage", ""), Launcher: l})
	if ctx := dbgDo(r, "POST", "/runs/r_1/replay", ""); ctx.Response.StatusCode() != 409 {
		t.Fatalf("faulted run: %d", ctx.Response.StatusCode())
	}
	r = debugServer(t, Services{Index: debugIndex(true, "clean", "")})
	if ctx := dbgDo(r, "POST", "/runs/r_1/replay", ""); ctx.Response.StatusCode() != 503 {
		t.Fatalf("no launcher: %d", ctx.Response.StatusCode())
	}
}

func TestHoldsAndRelease(t *testing.T) {
	hc := &fakeHolds{status: schemas.HoldStatus{Holds: []schemas.Hold{{Slot: "fp-slot-1", Call: 3, Reason: "call 3"}}, HeldMS: 1500}}
	l := &fakeLauncher{}
	r := debugServer(t, Services{Index: debugIndex(false, "clean", ""), Launcher: l, Holds: hc})
	ctx := dbgDo(r, "GET", "/runs/r_1/holds", "")
	var st schemas.HoldStatus
	_ = json.Unmarshal(ctx.Response.Body(), &st)
	if len(st.Holds) != 1 || st.HeldMS != 1500 || st.RunID != "r_1" {
		t.Fatalf("holds = %+v", st)
	}
	if ctx := dbgDo(r, "POST", "/runs/r_1/holds/release", `{"call":3,"action":"edit","response":"done"}`); ctx.Response.StatusCode() != 204 {
		t.Fatalf("release %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if len(hc.released) != 1 || hc.released[0].Response != "done" {
		t.Fatalf("released = %+v", hc.released)
	}
	if ctx := dbgDo(r, "POST", "/runs/r_1/holds/release", `{"call":4,"action":"continue"}`); ctx.Response.StatusCode() != 409 {
		t.Fatalf("release of a call not held: %d", ctx.Response.StatusCode())
	}
	if ctx := dbgDo(r, "POST", "/runs/r_1/holds/release", `{"call":3,"action":"jump"}`); ctx.Response.StatusCode() != 400 {
		t.Fatalf("bad action: %d", ctx.Response.StatusCode())
	}
	if ctx := dbgDo(r, "POST", "/runs/r_1/debug", `{"breakpoints":{"calls":[2]},"budget_usd":0.5}`); ctx.Response.StatusCode() != 201 {
		t.Fatalf("debug run %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if d := l.planned.Debug; d.Mode != schemas.DebugStep || d.Cassette != nil || d.Breakpoints.Calls[0] != 2 {
		t.Fatalf("debug spec = %+v", d)
	}
}

// N133: a replay of a run under a model (gateway layer) fault is exact: the
// cassette carries the faulted answers. It is allowed, the child keeps the
// clean condition (no second fault is armed) and records the source one.
func TestReplayAllowedForModelFaultRun(t *testing.T) {
	l := &fakeLauncher{}
	r := debugServer(t, Services{Index: debugIndex(true, schemas.GenericProbeModelBrownout, ""), Launcher: l})
	ctx := dbgDo(r, "POST", "/runs/r_1/replay", "")
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("replay of a model-fault run: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	exp := l.planned
	if len(exp.Conditions) != 1 || exp.Conditions[0].ID != schemas.ConditionClean || exp.Primary.Condition != schemas.ConditionClean {
		t.Fatalf("child conditions = %+v, primary %+v; want clean only", exp.Conditions, exp.Primary)
	}
	if exp.Debug == nil || exp.Debug.FromCondition != schemas.GenericProbeModelBrownout {
		t.Fatalf("debug spec = %+v; want from_condition %s", exp.Debug, schemas.GenericProbeModelBrownout)
	}
}

// N133: Fork and Debug go live after the fork point and plan the clean
// condition, so they would not repeat a fault: refused with the reason.
func TestForkRefusedForFaultRun(t *testing.T) {
	l := &fakeLauncher{}
	r := debugServer(t, Services{Index: debugIndex(true, schemas.GenericProbeModelBrownout, ""), Launcher: l, Holds: &fakeHolds{}})
	for _, c := range []struct{ path, body string }{{"/runs/r_1/fork", `{"call":1,"message":"x","budget_usd":0.5}`}, {"/runs/r_1/debug", `{"breakpoints":{"every":true},"budget_usd":0.5}`}} {
		ctx := dbgDo(r, "POST", c.path, c.body)
		if ctx.Response.StatusCode() != fasthttp.StatusConflict {
			t.Fatalf("%s: %d %s", c.path, ctx.Response.StatusCode(), ctx.Response.Body())
		}
		if body := string(ctx.Response.Body()); !strings.Contains(body, "Fork and Debug need a run without a fault") || !strings.Contains(body, "model-brownout") {
			t.Fatalf("%s: reason %s", c.path, body)
		}
	}
	if l.planned != nil {
		t.Fatalf("a refused fork planned %+v", l.planned)
	}
}

// N133: tool faults are not in the cassette, so a replay would diverge.
func TestReplayRefusedForToolFaultRun(t *testing.T) {
	r := debugServer(t, Services{Index: debugIndex(true, schemas.GenericProbeBrokenTool, ""), Launcher: &fakeLauncher{}})
	ctx := dbgDo(r, "POST", "/runs/r_1/replay", "")
	if ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("replay of a tool-fault run: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if body := string(ctx.Response.Body()); !strings.Contains(body, "Replay needs a run without a fault or with a model fault") {
		t.Fatalf("reason %s", body)
	}
}

// N133: the run detail says which debug actions the run allows, by the same rule.
func TestRunDetailDebugAllowed(t *testing.T) {
	for _, c := range []struct {
		cond          string
		replay, fork  bool
		reasonContain string
	}{
		{schemas.ConditionClean, true, true, ""},
		{schemas.GenericProbeModelBrownout, true, false, "Fork and Debug need a run without a fault. This run ran under model-brownout."},
		{schemas.GenericProbeBrokenTool, false, false, "Replay needs a run without a fault or with a model fault"},
	} {
		idx := debugIndex(true, c.cond, "")
		r := router.New()
		NewRunsHandler(testMode, idx).RegisterRoutes(r)
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.SetRequestURI(APIPrefix + "/runs/r_1")
		ctx.Request.Header.SetMethod(fasthttp.MethodGet)
		r.Handler(ctx)
		if ctx.Response.StatusCode() != 200 {
			t.Fatalf("%s: %d %s", c.cond, ctx.Response.StatusCode(), ctx.Response.Body())
		}
		var got struct {
			DebugAllowed *RunDebugAllowed `json:"debug_allowed"`
		}
		_ = json.Unmarshal(ctx.Response.Body(), &got)
		a := got.DebugAllowed
		if a == nil || a.Replay != c.replay || a.Fork != c.fork || !strings.Contains(a.Reason, c.reasonContain) || (c.reasonContain == "" && a.Reason != "") {
			t.Fatalf("%s: debug_allowed = %+v", c.cond, a)
		}
	}
}
