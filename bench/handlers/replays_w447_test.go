package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/replayrec"
	"github.com/maximhq/bifrost/bench/shadowreplay"
	"github.com/valyala/fasthttp"
)

func replayEnv(t *testing.T) (*replaysHandler, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "benchmarks"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &replaysHandler{env: OrgEnv{Cfg: &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}}}, root
}

func TestToInboxIngests(t *testing.T) {
	h, root := replayEnv(t)
	s := "s1"
	rec, err := replayrec.Build("prod", []replayrec.Row{{ID: "r1", Timestamp: time.Now(), SessionID: &s, Status: "success",
		RawRequest:  `{"messages":[{"role":"user","content":"Cancel order 7."}]}`,
		RawResponse: `{"choices":[{"message":{"role":"assistant","content":"Done, order 7 is cancelled."},"finish_reason":"stop"}]}`}})
	if err != nil {
		t.Fatal(err)
	}
	if err := shadowreplay.SaveRecording(root, rec); err != nil {
		t.Fatal(err)
	}
	d := 0
	rep := &shadowreplay.RunReport{ID: "j1", Mode: "fixed", Candidate: "openai/gpt-4o-mini", Note: shadowreplay.Note, Counts: map[string]int{},
		Sessions: []shadowreplay.SessionResult{{Recording: rec.ID, SessionID: "s1", Final: shadowreplay.StateDifferentText, DivergedAt: &d,
			Steps: []shadowreplay.StepResult{{State: shadowreplay.StateDifferentText}}}}}
	if err := shadowreplay.SaveReport(root, rep); err != nil {
		t.Fatal(err)
	}
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.SetUserValue("id", "j1")
	ctx.SetUserValue("recording", rec.ID)
	h.toInbox(&ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var res struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(ctx.Response.Body(), &res); err != nil || res.ItemID == "" {
		t.Fatalf("answer = %s", ctx.Response.Body())
	}
	got, err := shadowreplay.LoadReport(root, "j1")
	if err != nil || got.Sessions[0].InboxItem != res.ItemID {
		t.Fatalf("inbox item not kept on the report: %+v %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "inbox", "items.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "shadow replay: different_response at step 1 with openai/gpt-4o-mini") {
		t.Fatalf("reason missing from the inbox item: %s", b)
	}

	// An unknown session is a 404.
	var miss fasthttp.RequestCtx
	miss.SetUserValue("id", "j1")
	miss.SetUserValue("recording", "rec_nope")
	h.toInbox(&miss)
	if miss.Response.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("unknown session: status %d", miss.Response.StatusCode())
	}
}

func TestReplaySettingsDefaultOff(t *testing.T) {
	h, root := replayEnv(t)
	var ctx fasthttp.RequestCtx
	h.getSettings(&ctx)
	if !strings.Contains(string(ctx.Response.Body()), `"enabled":false`) {
		t.Fatalf("default settings = %s", ctx.Response.Body())
	}
	var put fasthttp.RequestCtx
	put.Request.SetBody([]byte(`{"enabled":true,"max_sessions_per_run":50,"max_usd_per_run":10}`))
	h.putSettings(&put)
	if put.Response.StatusCode() != fasthttp.StatusOK || shadowreplay.CheckEnabled(root) != nil {
		t.Fatalf("turn on: %d %s", put.Response.StatusCode(), put.Response.Body())
	}
	var bad fasthttp.RequestCtx
	bad.Request.SetBody([]byte(`{"enabled":true,"max_sessions_per_run":5000,"max_usd_per_run":10}`))
	h.putSettings(&bad)
	if bad.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("a cap over the limit: %d", bad.Response.StatusCode())
	}
}
