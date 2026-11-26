package handlers

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// A made-up Claude Code session (never a real file): a home folder path, an
// API key and an email are planted to prove the stored copy is cleaned.
const w445PlantedKey = "sk-ant0123456789abcdefghijkl"

func w445Session() []byte {
	return []byte(`{"type":"user","sessionId":"imp-1","uuid":"u1","cwd":"/home/planted-user/shop","gitBranch":"main","message":{"role":"user","content":"Refund A-1, my key is ` + w445PlantedKey + ` and mail is planted@example.com"}}
{"type":"assistant","sessionId":"imp-1","uuid":"a1","message":{"role":"assistant","model":"claude-test-1","content":[{"type":"tool_use","id":"t1","name":"refund_order","input":{"order_id":"A-1","path":"/home/planted-user/shop/a.txt"}}]}}
{"type":"user","sessionId":"imp-1","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Error: no such order","is_error":true}]}}
{"type":"assistant","sessionId":"imp-1","uuid":"a2","message":{"role":"assistant","model":"claude-test-1","content":[{"type":"text","text":"I could not refund it."}]}}
`)
}

// newW445Client serves the inbox routes with a server body cap large enough
// to reach the route's own 32 MB check.
func newW445Client(t *testing.T, inbox InboxSource) *testClient {
	t.Helper()
	r := router.New()
	h := NewInboxHandler(schemas.ModeLive, inbox, lib.RequireWriteToken(schemas.ModeLive, testToken))
	h.RegisterRoutes(r, lib.BodyLimit(schemas.ModeLive, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: 64 << 20}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestImportSessionRoute(t *testing.T) {
	svc := newInboxService(t, false)
	c := newW445Client(t, svc)
	hdr := authed("application/octet-stream")
	hdr["X-File-Name"] = "session.jsonl"

	if resp := c.do(t, "POST", InboxImportSessionPath, w445Session(), map[string]string{"X-File-Name": "a.jsonl"}); resp.StatusCode() != 401 {
		t.Errorf("no write token = %d %s", resp.StatusCode(), resp.Body())
	}
	big := make([]byte, SessionImportMaxBytes+1)
	for i := range big {
		big[i] = ' '
	}
	if resp := c.do(t, "POST", InboxImportSessionPath, big, hdr); resp.StatusCode() != 413 || !strings.Contains(string(resp.Body()), "larger than 32 MB") {
		t.Errorf("over 32 MB = %d %.200s", resp.StatusCode(), resp.Body())
	}
	chat := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`)
	if resp := c.do(t, "POST", InboxImportSessionPath, chat, hdr); resp.StatusCode() != 422 || !strings.Contains(string(resp.Body()), "This is not a Claude Code or Codex session file.") {
		t.Errorf("chat file = %d %s", resp.StatusCode(), resp.Body())
	}
	resp := c.do(t, "POST", InboxImportSessionPath, w445Session(), hdr)
	if resp.StatusCode() != 200 {
		t.Fatalf("import = %d %s", resp.StatusCode(), resp.Body())
	}
	var res schemas.InboxIngestResult
	if err := json.Unmarshal(resp.Body(), &res); err != nil || !res.Created || !traceinbox.ValidItemID(res.ItemID) {
		t.Fatalf("import result = %+v (%v)", res, err)
	}
	sess, err := svc.Inbox.Store.Session(res.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Format != "claude_code" || len(sess.ToolCalls) != 1 || sess.Workspace == nil {
		t.Errorf("stored session = %+v", sess)
	}
	// Nothing planted survives anywhere in the inbox folder.
	dir := svc.Inbox.Store.(*traceinbox.FileStore).Dir()
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		for _, planted := range []string{w445PlantedKey, "planted@example.com", "planted-user"} {
			if strings.Contains(string(data), planted) {
				t.Errorf("%s keeps %q", p, planted)
			}
		}
		return nil
	})
}

func TestInboxProposalRoute(t *testing.T) {
	svc := newInboxService(t, false)
	c := newW445Client(t, svc)
	hdr := authed("application/octet-stream")
	resp := c.do(t, "POST", InboxImportSessionPath, w445Session(), hdr)
	var res schemas.InboxIngestResult
	if err := json.Unmarshal(resp.Body(), &res); err != nil || res.ItemID == "" {
		t.Fatalf("import = %d %s", resp.StatusCode(), resp.Body())
	}
	path := strings.Replace(InboxProposalPath, "{id}", res.ItemID, 1)
	if resp := c.do(t, "GET", path, nil, nil); resp.StatusCode() != 404 || !strings.Contains(string(resp.Body()), "No draft yet.") {
		t.Errorf("no draft = %d %s", resp.StatusCode(), resp.Body())
	}
	file := filepath.Join(svc.Inbox.Store.(*traceinbox.FileStore).Dir(), traceinbox.ItemsDir, res.ItemID, ProposalFile)
	if err := os.WriteFile(file, []byte(`{"item":"`+res.ItemID+`","origin":"model"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if resp := c.do(t, "GET", path, nil, nil); resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"origin":"model"`) {
		t.Errorf("draft = %d %s", resp.StatusCode(), resp.Body())
	}
}
