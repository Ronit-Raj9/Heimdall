package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livefeed"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Live screen and takeover (Z03): the liveview org area over one org's hub,
// and the worker route that feeds it.

var liveJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'l', 'i', 'v', 'e'}

// liveCall sends one request through the liveview area of org.
func liveCall(t *testing.T, org, method, uri, body string, headers map[string]string) *fasthttp.Response {
	t.Helper()
	orgAreasMu.Lock()
	area := orgAreas["liveview"]
	orgAreasMu.Unlock()
	if area == nil {
		t.Fatal("liveview area not registered")
	}
	r := router.New()
	area(OrgEnv{Mode: testMode, Services: Services{OrgID: org}, Write: lib.RequireWriteToken(testMode, testToken),
		Common: []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)}}, r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	for k, v := range headers {
		ctx.Request.Header.Set(k, v)
	}
	ctx.Request.SetBodyString(body)
	r.Handler(&ctx)
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	return resp
}

func TestRunLiveStatusAndFrame(t *testing.T) {
	org := "z03-live-" + t.Name()
	resp := liveCall(t, org, "GET", APIPrefix+"/runs/r_live1/live", "", nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"live":false`) {
		t.Fatalf("before any frame: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := liveCall(t, org, "GET", APIPrefix+"/runs/r_live1/live/frame", "", nil); resp.StatusCode() != 404 {
		t.Fatalf("frame before any: %d", resp.StatusCode())
	}
	livefeed.For(org).Publish(schemas.LiveFrame{RunID: "r_live1", Service: "browser", Mime: "image/jpeg", Data: liveJPEG})
	resp = liveCall(t, org, "GET", APIPrefix+"/runs/r_live1/live", "", nil)
	var st RunLiveStatus
	if err := json.Unmarshal(resp.Body(), &st); err != nil || !st.Live || st.Service != "browser" || st.TakeoverOK || st.FrameURL == "" {
		t.Fatalf("status %+v (%v): %s", st, err, resp.Body())
	}
	resp = liveCall(t, org, "GET", APIPrefix+"/runs/r_live1/live/frame", "", nil)
	if resp.StatusCode() != 200 || string(resp.Body()) != string(liveJPEG) || string(resp.Header.ContentType()) != "image/jpeg" {
		t.Fatalf("frame: %d %s", resp.StatusCode(), resp.Header.ContentType())
	}
	if resp := liveCall(t, "z03-other-org", "GET", APIPrefix+"/runs/r_live1/live/frame", "", nil); resp.StatusCode() != 404 {
		t.Fatalf("another org sees the frame: %d", resp.StatusCode())
	}
}

func TestRunTakeoverRules(t *testing.T) {
	org := "z03-live-" + t.Name()
	hub := livefeed.For(org)
	click := `{"action":"click","x":10,"y":20}`
	w := authed("application/json")
	if resp := liveCall(t, org, "POST", APIPrefix+"/runs/r_t/takeover", click, nil); resp.StatusCode() != 401 {
		t.Fatalf("no write token: %d", resp.StatusCode())
	}
	if resp := liveCall(t, org, "POST", APIPrefix+"/runs/r_t/takeover", click, w); resp.StatusCode() != 409 {
		t.Fatalf("not live: %d %s", resp.StatusCode(), resp.Body())
	}
	hub.Publish(schemas.LiveFrame{RunID: "r_t", Service: "desktop", Data: liveJPEG, TakeoverOK: true})
	if resp := liveCall(t, org, "POST", APIPrefix+"/runs/r_t/takeover", click, w); resp.StatusCode() != 403 {
		t.Fatalf("not a debug run: %d %s", resp.StatusCode(), resp.Body())
	}
	hub.Publish(schemas.LiveFrame{RunID: "r_t", Service: "desktop", Data: liveJPEG, Debug: true})
	if resp := liveCall(t, org, "POST", APIPrefix+"/runs/r_t/takeover", click, w); resp.StatusCode() != 403 {
		t.Fatalf("no input command: %d %s", resp.StatusCode(), resp.Body())
	}
	hub.Publish(schemas.LiveFrame{RunID: "r_t", Service: "desktop", Data: liveJPEG, Debug: true, TakeoverOK: true})
	for _, bad := range []string{`{"action":"rm"}`, `{"action":"type"}`, `{"action":"click","x":1,"y":2,"extra":1}`, `nope`} {
		if resp := liveCall(t, org, "POST", APIPrefix+"/runs/r_t/takeover", bad, w); resp.StatusCode() != 400 {
			t.Errorf("bad input %s: %d", bad, resp.StatusCode())
		}
	}
	resp := liveCall(t, org, "POST", APIPrefix+"/runs/r_t/takeover", click, w)
	var a schemas.HumanAction
	if resp.StatusCode() != 202 || json.Unmarshal(resp.Body(), &a) != nil || a.Seq != 1 || a.Service != "desktop" {
		t.Fatalf("queue: %d %s", resp.StatusCode(), resp.Body())
	}
	if got := hub.Take("r_t"); len(got) != 1 || got[0].X != 10 {
		t.Fatalf("queued %+v", got)
	}
}

// liveWorkerAPI is a worker API that answers LiveLease.
type liveWorkerAPI struct {
	fakeWorkerAPI
	debug bool
	err   error
}

func (f *liveWorkerAPI) LiveLease(context.Context, string, string) (bool, error) {
	return f.debug, f.err
}

func TestWorkerLiveFrame(t *testing.T) {
	frame := func(runID string, data []byte, takeover bool) string {
		b, _ := json.Marshal(map[string]any{"worker_id": "w1", "run_id": runID, "service": "desktop", "data": base64.StdEncoding.EncodeToString(data), "takeover_ok": takeover, "debug": true})
		return string(b)
	}
	uri := "/api/bench/workers/leases/l_1/live-frame"
	plain := &liveWorkerAPI{}
	if resp := workerCall(t, plain, "POST", uri, "", frame("r_w1", liveJPEG, true)); resp.StatusCode() != 401 {
		t.Fatalf("no token: %d", resp.StatusCode())
	}
	if resp := workerCall(t, &fakeWorkerAPI{}, "POST", uri, testWorkerToken, frame("r_w1", liveJPEG, true)); resp.StatusCode() != 503 {
		t.Fatalf("api without LiveLease: %d", resp.StatusCode())
	}
	for _, body := range []string{frame("../x", liveJPEG, false), frame("r_w1", []byte("not an image"), false), frame("r_w1", nil, false)} {
		if resp := workerCall(t, plain, "POST", uri, testWorkerToken, body); resp.StatusCode() != 400 {
			t.Errorf("bad frame: %d %s", resp.StatusCode(), resp.Body())
		}
	}
	resp := workerCall(t, plain, "POST", uri, testWorkerToken, frame("r_w1", liveJPEG, true))
	if resp.StatusCode() != 200 {
		t.Fatalf("frame: %d %s", resp.StatusCode(), resp.Body())
	}
	f, ok := livefeed.For("").Frame("r_w1")
	if !ok || f.Debug || f.TakeoverOK || f.Mime != "image/jpeg" {
		t.Fatalf("published %+v %v: the worker may not claim a debug run", f, ok)
	}

	debug := &liveWorkerAPI{debug: true}
	if resp := workerCall(t, debug, "POST", uri, testWorkerToken, frame("r_w2", liveJPEG, true)); resp.StatusCode() != 200 {
		t.Fatalf("debug frame: %d", resp.StatusCode())
	}
	if _, err := livefeed.For("").Queue("r_w2", schemas.HumanAction{Action: "key", Combo: "ctrl+s"}); err != nil {
		t.Fatal(err)
	}
	resp = workerCall(t, debug, "POST", uri, testWorkerToken, frame("r_w2", liveJPEG, true))
	var reply schemas.LiveFrameReply
	if err := json.Unmarshal(resp.Body(), &reply); err != nil || len(reply.Inputs) != 1 || reply.Inputs[0].Combo != "ctrl+s" {
		t.Fatalf("reply %s (%v)", resp.Body(), err)
	}

	gone := &liveWorkerAPI{err: schemas.ErrLeaseGone}
	if resp := workerCall(t, gone, "POST", uri, testWorkerToken, frame("r_w3", liveJPEG, false)); resp.StatusCode() != 410 {
		t.Fatalf("lost lease: %d", resp.StatusCode())
	}
	if _, ok := livefeed.For("").Frame("r_w3"); ok {
		t.Fatal("a lost lease's frame was published")
	}
}
