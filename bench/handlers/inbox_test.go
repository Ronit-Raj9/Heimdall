package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

const inboxToken = "inbox-push-token"

// newInboxService is a real inbox over a temporary project with an mcp
// suite and a webhook source (2 pushes a minute, 8 KiB bodies).
func newInboxService(t *testing.T, withHook bool) *traceinbox.Service {
	t.Helper()
	root := t.TempDir()
	bench := filepath.Join(root, "benchmarks")
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "support", Template: scaffold.TemplateToolUse})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(filepath.Join(bench, "support"), files, false); err != nil {
		t.Fatal(err)
	}
	cfg := &traceinbox.Config{}
	if withHook {
		cfg.Sources = []traceinbox.Source{{Name: "hook", Kind: traceinbox.KindWebhook, TokenEnv: "INBOX_TOKEN", RatePerMinute: 2, MaxBytes: 8 << 10}}
	}
	ib := traceinbox.New(filepath.Join(root, traceinbox.Dir), cfg, bench)
	ib.ProjectRoot = root
	ib.Getenv = func(k string) string {
		if k == "INBOX_TOKEN" {
			return inboxToken
		}
		return ""
	}
	return traceinbox.NewService(ib)
}

func newInboxClient(t *testing.T, mode schemas.Mode, inbox InboxSource) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	h := NewInboxHandler(mode, inbox, lib.RequireWriteToken(mode, testToken))
	h.now = func() time.Time { return time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC) }
	h.RegisterRoutes(r, lib.BodyLimit(mode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func pushBody(id, order string) []byte {
	conv := map[string]any{"id": id, "status": "failed", "messages": []any{
		map[string]any{"role": "user", "content": "Refund order " + order + " please, key sk-live1234567890abcdefXYZ"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "c1", "type": "function",
			"function": map[string]any{"name": "issue_refund", "arguments": `{"order":"` + order + `"}`}}}},
		map[string]any{"role": "tool", "tool_call_id": "c1", "content": "Error: timeout after 30s"},
		map[string]any{"role": "assistant", "content": "Done."},
	}}
	b, _ := json.Marshal(map[string]any{"trace": conv, "reason": "thumbs down", "source": "support-widget", "link": "https://obs.example/t/" + id})
	return b
}

func inboxAuth() map[string]string {
	return map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + inboxToken}
}

func TestInboxRoutes(t *testing.T) {
	svc := newInboxService(t, true)
	c := newInboxClient(t, schemas.ModeLive, svc)
	ingest := APIPrefix + "/inbox/ingest"

	// The webhook: its own token, never the write token.
	for _, tc := range []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"no token", map[string]string{"Content-Type": "application/json"}, 401},
		{"write token is not the inbox token", authed("application/json"), 401},
		{"wrong token", map[string]string{"Authorization": "Bearer nope"}, 401},
	} {
		if resp := c.do(t, "POST", ingest, pushBody("t-1", "A-1"), tc.headers); resp.StatusCode() != tc.status {
			t.Errorf("%s: %d %s", tc.name, resp.StatusCode(), resp.Body())
		}
	}
	resp := c.do(t, "POST", ingest, pushBody("t-1", "A-1"), inboxAuth())
	if resp.StatusCode() != 201 {
		t.Fatalf("push = %d %s", resp.StatusCode(), resp.Body())
	}
	var pushed schemas.InboxIngestResult
	_ = json.Unmarshal(resp.Body(), &pushed)
	if !pushed.Created || !traceinbox.ValidItemID(pushed.ItemID) {
		t.Fatalf("push result = %+v", pushed)
	}
	if resp := c.do(t, "POST", ingest, pushBody("t-2", "A-2"), inboxAuth()); resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"occurrences":2`) {
		t.Errorf("repeat push = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "POST", ingest, pushBody("t-3", "A-3"), inboxAuth()); resp.StatusCode() != 429 || string(resp.Header.Peek("Retry-After")) == "" {
		t.Errorf("third push in a minute = %d %s, want 429", resp.StatusCode(), resp.Body())
	}
	big := []byte(`{"trace":{"messages":[{"role":"user","content":"` + strings.Repeat("x", 9<<10) + `"}]}}`)
	if resp := c.do(t, "POST", ingest, big, inboxAuth()); resp.StatusCode() != 413 {
		t.Errorf("oversized push = %d", resp.StatusCode())
	}

	id := pushed.ItemID
	tests := []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
		contains                 string
	}{
		{"list", "GET", APIPrefix + "/inbox", "", nil, 200, `"occurrences":2`},
		{"list new", "GET", APIPrefix + "/inbox?state=new", "", nil, 200, id},
		{"list bad state", "GET", APIPrefix + "/inbox?state=open", "", nil, 400, "state must be one of"},
		{"item", "GET", APIPrefix + "/inbox/" + id, "", nil, 200, `"preview":"# Flagged session`},
		{"item unknown", "GET", APIPrefix + "/inbox/inc-000000000000", "", nil, 404, "not_found"},
		{"item bad id", "GET", APIPrefix + "/inbox/..%2fetc", "", nil, 404, "not_found"},
		{"accept no token", "POST", APIPrefix + "/inbox/" + id + "/accept", `{"suite":"support"}`, map[string]string{"Content-Type": "application/json"}, 401, "unauthorized"},
		{"accept no suite", "POST", APIPrefix + "/inbox/" + id + "/accept", `{}`, authed("application/json"), 400, "suite is required"},
		{"accept unknown field", "POST", APIPrefix + "/inbox/" + id + "/accept", `{"suite":"support","admin":true}`, authed("application/json"), 400, "unknown field"},
		{"accept bad kind", "POST", APIPrefix + "/inbox/" + id + "/accept", `{"suite":"support","kind":"browser"}`, authed("application/json"), 400, "want one of"},
		{"accept", "POST", APIPrefix + "/inbox/" + id + "/accept", `{"suite":"support","task_id":"refund-timeout"}`, authed("application/json"), 200, `"task":"support/refund-timeout"`},
		{"accept twice", "POST", APIPrefix + "/inbox/" + id + "/accept", `{"suite":"support"}`, authed("application/json"), 409, "conflict"},
		{"reject accepted", "POST", APIPrefix + "/inbox/" + id + "/reject", `{"reason":"x"}`, authed("application/json"), 409, "conflict"},
		{"snooze unknown", "POST", APIPrefix + "/inbox/inc-000000000000/snooze", `{"days":3}`, authed("application/json"), 404, "not_found"},
		{"list accepted", "GET", APIPrefix + "/inbox?state=accepted", "", nil, 200, `"suite":"support"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			if tc.body != "" {
				body = []byte(tc.body)
			}
			resp := c.do(t, tc.method, tc.path, body, tc.headers)
			if resp.StatusCode() != tc.status || !strings.Contains(string(resp.Body()), tc.contains) {
				t.Fatalf("%s %s = %d %s, want %d containing %q", tc.method, tc.path, resp.StatusCode(), resp.Body(), tc.status, tc.contains)
			}
		})
	}
}

func TestInboxWritesRefusedOutsideLive(t *testing.T) {
	svc := newInboxService(t, true)
	c := newInboxClient(t, schemas.ModeRecorded, svc)
	if resp := c.do(t, "POST", APIPrefix+"/inbox/ingest", pushBody("t-1", "A-1"), inboxAuth()); resp.StatusCode() != 403 {
		t.Errorf("ingest in recorded mode = %d", resp.StatusCode())
	}
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-000000000000/snooze", []byte(`{"days":1}`), authed("application/json")); resp.StatusCode() != 403 {
		t.Errorf("snooze in recorded mode = %d", resp.StatusCode())
	}
	if resp := c.do(t, "GET", APIPrefix+"/inbox", nil, nil); resp.StatusCode() != 200 {
		t.Errorf("list in recorded mode = %d", resp.StatusCode())
	}
}

func TestInboxOffAndMissing(t *testing.T) {
	c := newInboxClient(t, schemas.ModeLive, newInboxService(t, false))
	if resp := c.do(t, "POST", APIPrefix+"/inbox/ingest", pushBody("t-1", "A-1"), inboxAuth()); resp.StatusCode() != 503 || !strings.Contains(string(resp.Body()), "webhook is off") {
		t.Errorf("ingest without a webhook source = %d %s", resp.StatusCode(), resp.Body())
	}
	nc := newInboxClient(t, schemas.ModeLive, nil)
	for _, p := range []string{"/inbox", "/inbox/inc-000000000000"} {
		if resp := nc.do(t, "GET", APIPrefix+p, nil, nil); resp.StatusCode() != 503 {
			t.Errorf("GET %s without a source = %d", p, resp.StatusCode())
		}
	}
}

func TestInboxContract(t *testing.T) {
	svc := newInboxService(t, true)
	c := newInboxClient(t, schemas.ModeLive, svc)
	resp := c.do(t, "POST", APIPrefix+"/inbox/ingest", pushBody("t-1", "A-1"), inboxAuth())
	obj := decodeBody(t, resp)
	checkKeys(t, "InboxIngestResult", obj, loadKeys(t, "InboxIngestResult"), true)
	id := obj["item_id"].(string)
	accept := c.do(t, "POST", APIPrefix+"/inbox/"+id+"/accept", []byte(`{"suite":"support"}`), authed("application/json"))
	acc := decodeBody(t, accept)
	checkKeys(t, "InboxAcceptResult", acc, loadKeys(t, "InboxAcceptResult"), true)
	checkKeys(t, "InboxDraft", acc["draft"].(map[string]any), loadKeys(t, "InboxDraft"), false)
	c.do(t, "POST", APIPrefix+"/inbox/ingest", []byte(`{"trace":{"id":"t-9","messages":[{"role":"user","content":"Where is my parcel?"}]}}`), inboxAuth())
	l := decodeBody(t, c.do(t, "GET", APIPrefix+"/inbox", nil, nil))
	checkKeys(t, "InboxList", l, loadKeys(t, "InboxList"), true)
	for path, typ := range map[string]string{"items[]": "InboxItem", "items[].signature_parts": "InboxSignatureParts", "items[].traces[]": "InboxTraceRef",
		"items[].task": "InboxTaskLink", "sources[]": "InboxSourceStatus", "suites[]": "InboxSuite"} {
		for i, o := range objectsAt(t, l, path) {
			checkKeys(t, fmt.Sprintf("InboxList.%s #%d", path, i), o, loadKeys(t, typ), false)
		}
	}
	other := ""
	for _, it := range l["items"].([]any) {
		if m := it.(map[string]any); m["id"] != id {
			other = m["id"].(string)
		}
	}
	d := decodeBody(t, c.do(t, "GET", APIPrefix+"/inbox/"+id, nil, nil))
	checkKeys(t, "InboxItemDetail", d, loadKeys(t, "InboxItemDetail"), true)
	checkKeys(t, "InboxItem", d["item"].(map[string]any), loadKeys(t, "InboxItem"), false)
	a := decodeBody(t, c.do(t, "POST", APIPrefix+"/inbox/"+other+"/snooze", []byte(`{"days":2}`), authed("application/json")))
	checkKeys(t, "InboxActionResult", a, loadKeys(t, "InboxActionResult"), true)
}
