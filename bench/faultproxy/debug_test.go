package faultproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/replay"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	replayReq1 = `{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`
	replayReq2 = `{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"rec 1"},{"role":"user","content":"more"}]}`
	recorded1  = `{"id":"msg_rec1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"rec 1"}],"usage":{"input_tokens":10,"output_tokens":3}}`
)

func testCassette() *schemas.Cassette {
	return &schemas.Cassette{SourceRun: "r_src", Entries: []schemas.CassetteEntry{
		{Call: 1, ReqHash: replay.RequestHash([]byte(replayReq1)), Status: 200, Body: recorded1},
		{Call: 2, ReqHash: replay.RequestHash([]byte(replayReq2)), Status: 200, Stream: true,
			Body: `{"type":"message_start","message":{"id":"msg_rec2","usage":{"input_tokens":5,"output_tokens":0}}}` + "\n\n" + `{"type":"message_stop"}`},
	}}
}

func (env *testEnv) loadReplay(t *testing.T, slot string, rp schemas.ReplayPlan) {
	t.Helper()
	if err := env.client.PutCassette(context.Background(), slot, testCassette()); err != nil {
		t.Fatalf("PutCassette: %v", err)
	}
	env.setPlan(t, slot, schemas.FaultPlan{Mode: ModeReplay, RunID: "r_dbg", Replay: &rp})
}

func TestReplayServesRecordingAtNoCost(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.loadReplay(t, "slot-a", schemas.ReplayPlan{})
	resp, body := env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
	if resp.StatusCode != 200 || string(body) != recorded1 {
		t.Fatalf("call 1 = %d %s", resp.StatusCode, body)
	}
	streamed := strings.Replace(replayReq2, `"max_tokens":64,`, `"max_tokens":64,`, 1)
	resp, body = env.call(t, "POST", "/v1/messages", "auth-a", streamed)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "event: message_start\ndata: {\"type\":\"message_start\"") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("call 2 = %d %q", resp.StatusCode, body)
	}
	// A third call is past the recording: the run stops there.
	resp, body = env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
	if resp.StatusCode != 400 || !strings.Contains(string(body), "replay ended") {
		t.Fatalf("call 3 = %d %s", resp.StatusCode, body)
	}
	resp, _ = env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
	if resp.StatusCode != 400 {
		t.Fatalf("a stopped replay must refuse later calls, got %d", resp.StatusCode)
	}
	all := env.entries(t)
	if len(all) != 4 {
		t.Fatalf("ledger = %d lines", len(all))
	}
	if e := all[0]; e.Replay != schemas.ReplayHit || e.Upstream != schemas.LedgerUpstreamReplay || e.Action != ActionPass || e.ResponseID != "msg_rec1" {
		t.Fatalf("line 1 = %+v", e)
	}
	if e := all[1]; e.Replay != schemas.ReplayHit || e.ResponseID != "msg_rec2" {
		t.Fatalf("line 2 = %+v", e)
	}
	if e := all[2]; e.Replay != schemas.ReplayExhausted || e.Action != ActionReject {
		t.Fatalf("line 3 = %+v", e)
	}
	if e := all[3]; e.Operator != schemas.ReplayExhausted {
		t.Fatalf("line 4 = %+v", e)
	}
}

func TestReplayDivergencePolicies(t *testing.T) {
	changed := strings.Replace(replayReq1, `"hi"`, `"hello"`, 1)
	t.Run("stop", func(t *testing.T) {
		env := newTestEnv(t, UpstreamScripted, "")
		env.loadReplay(t, "slot-a", schemas.ReplayPlan{})
		resp, body := env.call(t, "POST", "/v1/messages", "auth-a", changed)
		if resp.StatusCode != 400 || !strings.Contains(string(body), "differs from the recorded run") {
			t.Fatalf("got %d %s", resp.StatusCode, body)
		}
		e := env.last(t)
		if e.Replay != schemas.ReplayDivergence || e.ReplayExpected != replay.RequestHash([]byte(replayReq1)) {
			t.Fatalf("ledger = %+v", e)
		}
	})
	t.Run("live", func(t *testing.T) {
		env := newTestEnv(t, UpstreamScripted, "")
		env.loadReplay(t, "slot-a", schemas.ReplayPlan{OnDivergence: schemas.DivergenceLive})
		resp, body := env.call(t, "POST", "/v1/messages", "auth-a", changed)
		if resp.StatusCode != 200 || !strings.Contains(string(body), scriptedText) {
			t.Fatalf("got %d %s", resp.StatusCode, body)
		}
		if e := env.last(t); e.Replay != schemas.ReplayDivergence || e.Upstream == schemas.LedgerUpstreamReplay {
			t.Fatalf("ledger = %+v", e)
		}
		// Live for good: even a matching call 2 goes to the upstream now.
		_, body = env.call(t, "POST", "/v1/messages", "auth-a", replayReq2)
		if !strings.Contains(string(body), scriptedText) {
			t.Fatalf("call 2 = %s", body)
		}
	})
	t.Run("serve", func(t *testing.T) {
		env := newTestEnv(t, UpstreamScripted, "")
		env.loadReplay(t, "slot-a", schemas.ReplayPlan{OnDivergence: schemas.DivergenceServe})
		_, body := env.call(t, "POST", "/v1/messages", "auth-a", changed)
		if string(body) != recorded1 {
			t.Fatalf("got %s", body)
		}
		if e := env.last(t); e.Replay != schemas.ReplayDivergence || e.Upstream != schemas.LedgerUpstreamReplay {
			t.Fatalf("ledger = %+v", e)
		}
	})
}

func TestForkEditsThenGoesLive(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		env := newTestEnv(t, UpstreamScripted, "")
		env.loadReplay(t, "slot-a", schemas.ReplayPlan{UntilCall: 2, EditResponse: "operator answer"})
		env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
		_, body := env.call(t, "POST", "/v1/messages", "auth-a", replayReq2)
		if !strings.Contains(string(body), "operator answer") {
			t.Fatalf("fork call = %s", body)
		}
		if e := env.last(t); e.Replay != schemas.ReplayEdited {
			t.Fatalf("ledger = %+v", e)
		}
		_, body = env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
		if !strings.Contains(string(body), scriptedText) {
			t.Fatalf("after the fork the run is live: %s", body)
		}
	})
	t.Run("message", func(t *testing.T) {
		seen := make(chan *http.Request, 4)
		up := fakeUpstream(t, "application/json", recorded1, seen)
		env := newTestEnvConfig(t, Config{Upstream: up.URL, UpstreamKey: "k"})
		env.loadReplay(t, "slot-a", schemas.ReplayPlan{UntilCall: 1, EditMessage: "try the tests first"})
		env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
		select {
		case r := <-seen:
			_ = r
		case <-time.After(5 * time.Second):
			t.Fatal("the fork call never reached the upstream")
		}
		if e := env.last(t); e.Replay != schemas.ReplayEdited || e.Upstream != schemas.LedgerUpstreamReal {
			t.Fatalf("ledger = %+v", e)
		}
	})
}

func TestReplayPlanNeedsCassette(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	err := env.client.SetPlan(context.Background(), "slot-a", schemas.FaultPlan{Mode: ModeReplay, Replay: &schemas.ReplayPlan{}})
	if err == nil || !strings.Contains(err.Error(), "needs a cassette") {
		t.Fatalf("err = %v", err)
	}
	for _, p := range []schemas.FaultPlan{
		{Mode: ModeReplay},
		{Mode: ModeNone, Replay: &schemas.ReplayPlan{}},
		{Mode: ModeReplay, Replay: &schemas.ReplayPlan{OnDivergence: "maybe"}},
		{Mode: ModeReplay, Replay: &schemas.ReplayPlan{EditResponse: "x"}},
		{Mode: ModeNone, Hold: &schemas.Breakpoints{Calls: []int{0}}},
	} {
		if ValidatePlan(p) == nil {
			t.Errorf("plan %+v must be refused", p)
		}
	}
}

func TestCassetteSurvivesRestart(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	if err := env.client.PutCassette(context.Background(), "slot-a", testCassette()); err != nil {
		t.Fatal(err)
	}
	d, err := newDebugState(CassettesPath(env.ledger))
	if err != nil {
		t.Fatal(err)
	}
	if !d.hasCassette("slot-a") {
		t.Fatal("cassette lost on restart")
	}
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone})
	d, _ = newDebugState(CassettesPath(env.ledger))
	if d.hasCassette("slot-a") {
		t.Fatal("a non-replay plan must drop the cassette")
	}
}

// waitHold polls the holds of run r_dbg until one is pending.
func (env *testEnv) waitHold(t *testing.T) schemas.Hold {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := env.client.Holds(context.Background(), "r_dbg")
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Holds) > 0 {
			return st.Holds[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no request was held")
	return schemas.Hold{}
}

func TestBreakpointHoldAndRelease(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone, RunID: "r_dbg", Hold: &schemas.Breakpoints{Calls: []int{1}}})
	var wg sync.WaitGroup
	results := make([]string, 3)
	call := func(i int, body string) {
		defer wg.Done()
		_, b := env.call(t, "POST", "/v1/messages", "auth-a", body)
		results[i] = string(b)
	}
	wg.Add(1)
	go call(0, replayReq1)
	h := env.waitHold(t)
	if h.Call != 1 || h.Reason != "call 1" || h.Slot != "slot-a" || !strings.Contains(h.Request, `"hi"`) || h.Deadline.IsZero() {
		t.Fatalf("hold = %+v", h)
	}
	// Step: call 1 continues and call 2 is held too.
	if err := env.client.ReleaseHold(context.Background(), "slot-a", schemas.HoldRelease{Call: 1, Action: schemas.HoldStep}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !strings.Contains(results[0], scriptedText) {
		t.Fatalf("call 1 = %s", results[0])
	}
	wg.Add(1)
	go call(1, replayReq2)
	h = env.waitHold(t)
	if h.Call != 2 || h.Reason != "step" {
		t.Fatalf("hold 2 = %+v", h)
	}
	if err := env.client.ReleaseHold(context.Background(), "slot-a", schemas.HoldRelease{Call: 2, Action: schemas.HoldEdit, Response: "edited by hand"}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !strings.Contains(results[1], "edited by hand") {
		t.Fatalf("call 2 = %s", results[1])
	}
	st, _ := env.client.Holds(context.Background(), "r_dbg")
	if st.HeldMS <= 0 || len(st.Holds) != 0 {
		t.Fatalf("status = %+v", st)
	}
	// A release with no hold pending is a conflict.
	if err := env.client.ReleaseHold(context.Background(), "slot-a", schemas.HoldRelease{Call: 9, Action: schemas.HoldContinue}); err == nil {
		t.Fatal("release of no hold must fail")
	}
	all := env.entries(t)
	if all[0].Operator != schemas.HoldStep || all[0].HeldMS <= 0 || all[1].Operator != schemas.HoldEdit {
		t.Fatalf("ledger = %+v / %+v", all[0], all[1])
	}
}

func TestBreakpointAbortStopsRun(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone, RunID: "r_dbg", Hold: &schemas.Breakpoints{Every: true}})
	done := make(chan []byte, 1)
	go func() {
		_, b := env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
		done <- b
	}()
	env.waitHold(t)
	if err := env.client.ReleaseHold(context.Background(), "slot-a", schemas.HoldRelease{Call: 1, Action: schemas.HoldAbort}); err != nil {
		t.Fatal(err)
	}
	if b := <-done; !strings.Contains(string(b), "operator stopped") {
		t.Fatalf("abort answer = %s", b)
	}
	resp, _ := env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
	if resp.StatusCode != 400 {
		t.Fatalf("after abort = %d", resp.StatusCode)
	}
	st, _ := env.client.Holds(context.Background(), "r_dbg")
	if !st.Stopped {
		t.Fatal("status must report the stop")
	}
}

func TestBreakpointTimesOutAndAfterTool(t *testing.T) {
	env := newTestEnvConfig(t, Config{Upstream: UpstreamScripted, MaxHold: 50 * time.Millisecond})
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone, RunID: "r_dbg", Hold: &schemas.Breakpoints{AfterTools: []string{"bash"}}})
	// No tool result: not held.
	resp, _ := env.call(t, "POST", "/v1/messages", "auth-a", replayReq1)
	if resp.StatusCode != 200 || env.last(t).Operator != "" {
		t.Fatalf("plain call held: %+v", env.last(t))
	}
	withTool := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`
	resp, _ = env.call(t, "POST", "/v1/messages", "auth-a", withTool)
	if resp.StatusCode != 200 {
		t.Fatalf("held call = %d", resp.StatusCode)
	}
	if e := env.last(t); e.Operator != "timeout" || e.HeldMS < 40 {
		t.Fatalf("ledger = %+v", e)
	}
}

func TestHealthListsReplay(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	h, err := env.client.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !h.Supports(ModeReplay) {
		t.Fatalf("plan modes = %v", h.PlanModes)
	}
	b, _ := json.Marshal(h.PlanFields)
	if !strings.Contains(string(b), "hold") {
		t.Fatalf("plan fields = %s", b)
	}
}
