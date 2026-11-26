package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// iwieSession is a made-up Claude Code session (never a real session file)
// that plants a home folder name, an email and an API key.
const iwieSession = `{"type":"user","sessionId":"sess-iwie-1","uuid":"u1","parentUuid":null,"timestamp":"2026-11-20T10:00:01.000Z","cwd":"/home/planted-iwie/shop","isSidechain":false,"message":{"role":"user","content":"Refund order A-1001 for planted.iwie@example.org with key sk-live0123456789iwieabcd."}}
{"type":"assistant","sessionId":"sess-iwie-1","uuid":"a1","parentUuid":"u1","timestamp":"2026-11-20T10:00:02.000Z","cwd":"/home/planted-iwie/shop","isSidechain":false,"message":{"id":"msg_1","role":"assistant","model":"claude-test-1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"refund_order","input":{"order_id":"A-1001"}}]}}
{"type":"user","sessionId":"sess-iwie-1","uuid":"u2","parentUuid":"a1","timestamp":"2026-11-20T10:00:03.000Z","cwd":"/home/planted-iwie/shop","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"order not found","is_error":true}]}}
{"type":"assistant","sessionId":"sess-iwie-1","uuid":"a2","parentUuid":"u2","timestamp":"2026-11-20T10:00:05.000Z","cwd":"/home/planted-iwie/shop","isSidechain":false,"message":{"id":"msg_2","role":"assistant","model":"claude-test-1","stop_reason":"end_turn","content":[{"type":"text","text":"I could not refund the order."}]}}
`

// doRaw sends body as is (the upload route reads the raw file).
func (f *hostedFixture) doRaw(method, path, as string, hdr map[string]string, body []byte) resp {
	f.t.Helper()
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.Header.SetMethod(method)
	req.SetRequestURI("http://127.0.0.1" + path)
	req.Header.SetHost("127.0.0.1")
	if as != "" {
		req.Header.Set(testAsHeader, as)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.Header.SetContentType("application/octet-stream")
	req.SetBody(body)
	res := &fasthttp.Response{}
	if err := f.client.DoTimeout(req, res, 60*time.Second); err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp{status: res.StatusCode(), body: string(res.Body())}
}

// filesUnder returns the text of every regular file under root whose
// relative path starts with prefix.
func filesUnder(t *testing.T, root, prefix string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if !strings.HasPrefix(filepath.ToSlash(rel), prefix) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err == nil {
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

// Group E security review of the session upload route (W4-45): signed out
// is 401, a viewer or someone outside the organization is refused, a file
// over 32 MB is 413, and a member's upload lands only in the member's own
// organization, redacted before it is stored.
func TestHostedImportSessionAccess(t *testing.T) {
	f := newHostedFixture(t)
	const path = "/api/bench/inbox/import-session"
	body := []byte(iwieSession)

	if r := f.doRaw("POST", path, "", nil, body); r.status != fasthttp.StatusUnauthorized {
		t.Errorf("signed out: %d, want 401: %s", r.status, trim(r.body))
	}
	if r := f.doRaw("POST", path, "vera", nil, body); r.status != fasthttp.StatusForbidden {
		t.Errorf("viewer: %d, want 403: %s", r.status, trim(r.body))
	}
	for _, who := range []string{"bob", "mallory"} {
		r := f.doRaw("POST", path, who, map[string]string{tenancy.OrgHeader: f.acme.ID}, body)
		if r.status != fasthttp.StatusForbidden && r.status != fasthttp.StatusNotFound {
			t.Errorf("%s naming another organization: %d, want 403 or 404: %s", who, r.status, trim(r.body))
		}
	}
	big := bytes.Repeat([]byte("x"), 32<<20+1)
	if r := f.doRaw("POST", path, "alice", nil, big); r.status != fasthttp.StatusRequestEntityTooLarge {
		t.Errorf("a file over 32 MB: %d, want 413: %s", r.status, trim(r.body))
	}

	r := f.doRaw("POST", path, "alice", nil, body)
	if r.status != fasthttp.StatusOK {
		t.Fatalf("member upload: %d: %s", r.status, trim(r.body))
	}
	var res struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal([]byte(r.body), &res); err != nil || res.ItemID == "" {
		t.Fatalf("answer %s: %v", r.body, err)
	}
	pa, _ := f.env.layout.Paths(f.acme.ID)
	pb, _ := f.env.layout.Paths(f.beta.ID)
	stored := filesUnder(t, pa.ProjectRoot, "inbox/")
	found := false
	for name, text := range stored {
		if strings.Contains(text, res.ItemID) || strings.Contains(name, res.ItemID) {
			found = true
		}
		for _, planted := range []string{"planted-iwie", "planted.iwie@example.org", "sk-live0123456789iwieabcd"} {
			if strings.Contains(text, planted) {
				t.Errorf("%s holds %q unredacted", name, planted)
			}
		}
	}
	if !found {
		t.Fatalf("the item %s is not in the member's own inbox (%d files)", res.ItemID, len(stored))
	}
	for name, text := range filesUnder(t, pb.ProjectRoot, "inbox/") {
		if strings.Contains(text, res.ItemID) || strings.Contains(name, res.ItemID) {
			t.Errorf("the upload reached another organization: %s", name)
		}
	}
}
