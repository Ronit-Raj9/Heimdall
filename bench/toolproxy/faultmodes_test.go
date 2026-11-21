package toolproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestParseFaultResilienceModes(t *testing.T) {
	tests := []struct {
		name, in string
		wantErr  bool
	}{
		{name: "rate_limit", in: `{"mode":"rate_limit","limit":2,"window_s":10}`},
		{name: "rate_limit no window", in: `{"mode":"rate_limit","limit":2}`, wantErr: true},
		{name: "slow", in: `{"mode":"slow","delay_ms":1500}`},
		{name: "slow negative", in: `{"mode":"slow","delay_ms":-1}`, wantErr: true},
		{name: "flaky", in: `{"mode":"flaky","prob":0.3}`},
		{name: "flaky no prob", in: `{"mode":"flaky"}`, wantErr: true},
		{name: "flaky prob over one", in: `{"mode":"flaky","prob":1.5}`, wantErr: true},
		{name: "partial", in: `{"mode":"partial","keep":2}`},
		{name: "partial keep zero", in: `{"mode":"partial"}`, wantErr: true},
		{name: "auth_expired", in: `{"mode":"auth_expired","tool":"search","refresh":"login"}`},
		{name: "auth_expired no refresh", in: `{"mode":"auth_expired"}`, wantErr: true},
		{name: "tool_removed", in: `{"mode":"tool_removed","tool":"search"}`},
		{name: "tool_removed no tool", in: `{"mode":"tool_removed"}`, wantErr: true},
		{name: "tool_removed first tool called", in: `{"mode":"tool_removed","from_call":2}`},
		{name: "input_drift", in: `{"mode":"input_drift","tool":"search","rename":{"q":"query"}}`},
		{name: "input_drift no rename", in: `{"mode":"input_drift","tool":"search"}`, wantErr: true},
		{name: "duplicate_effect", in: `{"mode":"duplicate_effect"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseFault([]byte(tt.in)); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// fileWith returns a fault file follower holding fault, with a fake clock.
func fileWith(t *testing.T, fault string, now *time.Time) *faultFile {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fault.json")
	if err := os.WriteFile(p, []byte(fault), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFaultFile(p, schemas.NoopLogger{})
	f.now = func() time.Time { return *now }
	return f
}

func TestRateLimitWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	f := fileWith(t, `{"mode":"rate_limit","tool":"q","limit":2,"window_s":10}`, &now)
	var got []bool
	var retry int
	for range 3 {
		ft, hit := f.decide("fs", "q", "")
		got = append(got, hit)
		retry = ft.RetryAfterS
	}
	if fmt.Sprint(got) != "[false false true]" || retry != 10 {
		t.Fatalf("hits %v retry %d", got, retry)
	}
	now = now.Add(4 * time.Second)
	if ft, hit := f.decide("fs", "q", ""); !hit || ft.RetryAfterS != 6 {
		t.Fatalf("still limited: hit %v retry %d, want 6", hit, ft.RetryAfterS)
	}
	now = now.Add(7 * time.Second) // the window has freed both calls
	if _, hit := f.decide("fs", "q", ""); hit {
		t.Fatal("call after the window was refused")
	}
}

func TestFlakySeeded(t *testing.T) {
	run := func(nonce string) []bool {
		var out []bool
		for i := 1; i <= 200; i++ {
			out = append(out, flakyFails(nonce, i, 0.3))
		}
		return out
	}
	a, b := run("n1"), run("n1")
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("same nonce gave different coins")
	}
	fails := 0
	for _, v := range a {
		if v {
			fails++
		}
	}
	if fails < 35 || fails > 85 {
		t.Fatalf("%d of 200 failed at p=0.3", fails)
	}
	if fmt.Sprint(a) == fmt.Sprint(run("n2")) {
		t.Fatal("different nonces gave the same coins")
	}
}

func TestAuthExpiredUntilRefresh(t *testing.T) {
	now := time.Unix(0, 0)
	f := fileWith(t, `{"mode":"auth_expired","tool":"search","refresh":"login"}`, &now)
	if _, hit := f.decide("fs", "search", ""); !hit {
		t.Fatal("first call should be refused")
	}
	if _, hit := f.decide("fs", "login", ""); hit {
		t.Fatal("refresh call was faulted")
	}
	if _, hit := f.decide("fs", "search", ""); hit {
		t.Fatal("call after refresh was refused")
	}
}

func TestDriftCall(t *testing.T) {
	ft := schemas.ToolFault{Mode: FaultInputDrift, Rename: map[string]string{"q": "query"}}
	if _, stale := driftCall(ft, []byte(`{"q":"x"}`)); !stale {
		t.Fatal("old shape not refused")
	}
	out, stale := driftCall(ft, []byte(`{"query":"x","n":1}`))
	if stale || !strings.Contains(string(out), `"q":"x"`) || strings.Contains(string(out), "query") {
		t.Fatalf("new shape: %s stale %v", out, stale)
	}
	if out, stale := driftCall(ft, []byte(`not json`)); stale || string(out) != "not json" {
		t.Fatal("non-object arguments changed")
	}
	if msg := driftMessage(ft); !strings.Contains(msg, `"q" is now "query"`) || !strings.Contains(msg, "v2") {
		t.Fatalf("message %q", msg)
	}
}

func TestPartialValue(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"results":[1,2,3,4],"meta":{"tags":["a","b","c"]}}`), &v)
	out, ok := partialValue(v, 2)
	b, _ := json.Marshal(out)
	s := string(b)
	if !ok || !strings.Contains(s, `"results":[1,2]`) || !strings.Contains(s, `"tags":["a","b"]`) || !strings.Contains(s, `"truncated":true`) || !strings.Contains(s, "next_cursor") {
		t.Fatalf("partial = %s", s)
	}
	if _, ok := partialValue(map[string]any{"a": []any{1}}, 2); ok {
		t.Fatal("a short array was cut")
	}
}

func TestListingRemovedAndDrift(t *testing.T) {
	list := json.RawMessage(`{"tools":[{"name":"search","inputSchema":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}},{"name":"echo"}]}`)
	out, ok := rewriteListing(list, schemas.ToolFault{Mode: FaultToolRemoved, Tool: "search"})
	if !ok || strings.Contains(string(out), "search") || !strings.Contains(string(out), "echo") {
		t.Fatalf("removed: %s", out)
	}
	out, ok = rewriteListing(list, schemas.ToolFault{Mode: FaultInputDrift, Tool: "search", Rename: map[string]string{"q": "query"}})
	if !ok || !strings.Contains(string(out), `"query":{"type":"string"}`) || !strings.Contains(string(out), `"required":["query"]`) {
		t.Fatalf("drift: %s", out)
	}
}

func TestToolRemovedListingFromCall(t *testing.T) {
	now := time.Unix(0, 0)
	f := fileWith(t, `{"mode":"tool_removed","tool":"search","from_call":3}`, &now)
	if _, ok := f.listing("fs", ""); ok {
		t.Fatal("tool removed before from_call")
	}
	f.decide("fs", "search", "")
	f.decide("fs", "search", "")
	if _, ok := f.listing("fs", ""); !ok {
		t.Fatal("tool still listed after it was removed")
	}
	if _, hit := f.decide("fs", "search", ""); !hit {
		t.Fatal("third call not refused")
	}
}

func TestToolRemovedBindsFirstTool(t *testing.T) {
	now := time.Unix(0, 0)
	f := fileWith(t, `{"mode":"tool_removed","from_call":2}`, &now)
	if _, hit := f.decide("fs", "search", ""); hit {
		t.Fatal("first call refused")
	}
	if _, hit := f.decide("fs", "echo", ""); hit {
		t.Fatal("another tool was removed")
	}
	if ft, ok := f.listing("fs", ""); !ok || ft.Tool != "search" {
		t.Fatalf("listing fault %+v %v", ft, ok)
	}
	if _, hit := f.decide("fs", "search", ""); !hit {
		t.Fatal("removed tool still answered")
	}
}

// TestProxyResilienceModesMCP drives each mode end to end through the MCP
// path against the fake server.
func TestProxyResilienceModesMCP(t *testing.T) {
	const nonce = "n_res"
	tests := []struct {
		name, fault   string
		tool, args    string
		wantForwarded int
		wantText      []string
		wantErr       bool
		check         func(t *testing.T, tp *testProxy, hdr http.Header, r schemas.ToolCall)
	}{
		{name: "rate_limit", fault: `{"mode":"rate_limit","limit":1,"window_s":30,"retry_after_s":5,"nonce":"n_res"}`, tool: "echo", wantForwarded: 1,
			wantText: []string{"429 Too Many Requests", "Retry-After: 5"}, wantErr: true,
			check: func(t *testing.T, _ *testProxy, hdr http.Header, r schemas.ToolCall) {
				if hdr.Get("Retry-After") != "5" || r.RetryAfterS != 5 {
					t.Errorf("Retry-After header %q row %d", hdr.Get("Retry-After"), r.RetryAfterS)
				}
			}},
		{name: "slow", fault: `{"mode":"slow","delay_ms":2500,"calls":1,"nonce":"n_res"}`, tool: "echo", wantForwarded: 2, wantText: []string{"echo: hi"},
			check: func(t *testing.T, tp *testProxy, _ http.Header, r schemas.ToolCall) {
				if len(tp.holds) == 0 || tp.holds[0] != 2500*time.Millisecond {
					t.Errorf("holds %v", tp.holds)
				}
			}},
		{name: "flaky", fault: `{"mode":"flaky","prob":1,"calls":1,"nonce":"n_res"}`, tool: "echo", wantForwarded: 1, wantText: []string{"503"}, wantErr: true},
		{name: "partial", fault: `{"mode":"partial","keep":1,"calls":1,"nonce":"n_res"}`, tool: "list", wantForwarded: 2, wantText: []string{`truncated`, "next_cursor"}},
		{name: "auth_expired", fault: `{"mode":"auth_expired","tool":"echo","refresh":"login","nonce":"n_res"}`, tool: "echo", wantForwarded: 0, wantText: []string{"401", "call login"}, wantErr: true},
		{name: "tool_removed", fault: `{"mode":"tool_removed","tool":"echo","calls":1,"nonce":"n_res"}`, tool: "echo", wantForwarded: 1, wantText: []string{`"code":-32601`, "no longer offered"}, wantErr: true},
		{name: "input_drift", fault: `{"mode":"input_drift","tool":"echo","rename":{"text":"message"},"calls":1,"nonce":"n_res"}`, tool: "echo", wantForwarded: 1, wantText: []string{"400 Bad Request", `\"text\" is now \"message\"`}, wantErr: true},
		{name: "duplicate_effect", fault: `{"mode":"duplicate_effect","calls":1,"nonce":"n_res"}`, tool: "echo", wantForwarded: 3, wantText: []string{"echo: hi"},
			check: func(t *testing.T, _ *testProxy, _ http.Header, r schemas.ToolCall) {
				if r.Effects != 2 {
					t.Errorf("effects %d", r.Effects)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := &fakeMCP{}
			upstream := httptest.NewServer(up)
			defer upstream.Close()
			tp := newTestProxy(t, upstream.URL, ProtocolMCP)
			tp.srv.hold = func(_ context.Context, d time.Duration) error { tp.holds = append(tp.holds, d); return nil }
			tp.setFault(t, tt.fault)
			// The rate limit lets the first call through; every other mode
			// takes the first call, and the second is clean (calls 1) or
			// still faulted, which wantForwarded counts.
			first := callBody(1, tt.tool, "hi")
			if tt.tool == "list" {
				first = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"many","arguments":{}}}`
			}
			var hdr http.Header
			var body string
			if tt.name == "rate_limit" {
				post(t, tp.front.URL, "/s/fs/mcp", first, nil)
				_, hdr, body = post(t, tp.front.URL, "/s/fs/mcp", callBody(2, "echo", "hi"), nil)
			} else {
				_, hdr, body = post(t, tp.front.URL, "/s/fs/mcp", first, nil)
				post(t, tp.front.URL, "/s/fs/mcp", callBody(2, "echo", "again"), nil)
			}
			for _, w := range tt.wantText {
				if !strings.Contains(body, w) {
					t.Errorf("answer %s lacks %q", body, w)
				}
			}
			if got := len(up.toolCalls()); got != tt.wantForwarded {
				t.Errorf("tool server got %d calls %v, want %d", got, up.toolCalls(), tt.wantForwarded)
			}
			rows := tp.waitCallRows(t, 2)
			var faulted *schemas.ToolCall
			for i := range rows {
				if rows[i].Nonce == nonce {
					faulted = &rows[i]
					break
				}
			}
			if faulted == nil {
				t.Fatalf("no faulted row in %+v", rows)
			}
			if faulted.Fault != tt.name || faulted.IsError != tt.wantErr {
				t.Errorf("row %+v", *faulted)
			}
			if tt.check != nil {
				tt.check(t, tp, hdr, *faulted)
			}
		})
	}
}

func TestProxyInputDriftTranslatesNewShape(t *testing.T) {
	up := &fakeMCP{}
	upstream := httptest.NewServer(up)
	defer upstream.Close()
	tp := newTestProxy(t, upstream.URL, ProtocolMCP)
	tp.setFault(t, `{"mode":"input_drift","tool":"echo","rename":{"text":"message"},"nonce":"n_d"}`)
	_, _, list := post(t, tp.front.URL, "/s/fs/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if !strings.Contains(list, "echo") {
		t.Fatalf("listing %s", list)
	}
	_, _, body := post(t, tp.front.URL, "/s/fs/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}`, nil)
	if !strings.Contains(body, "echo: hello") {
		t.Fatalf("new-shape call not translated: %s", body)
	}
}

func TestProxyHTTPRateLimitAndDuplicate(t *testing.T) {
	var hits atomic.Int32
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[1,2,3]}`)
	})
	upstream := httptest.NewServer(api)
	defer upstream.Close()
	get := func(base string) (int, http.Header, string) {
		resp, err := http.Get(base + "/s/fs/items")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b)
	}

	tp := newTestProxy(t, upstream.URL, ProtocolHTTP)
	tp.setFault(t, `{"mode":"rate_limit","limit":1,"window_s":60,"nonce":"n_h"}`)
	if s, _, _ := get(tp.front.URL); s != 200 {
		t.Fatalf("first call %d", s)
	}
	s, hdr, body := get(tp.front.URL)
	if s != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" || !strings.Contains(body, "n_h") {
		t.Fatalf("second call %d %q %s", s, hdr.Get("Retry-After"), body)
	}

	hits.Store(0)
	tp2 := newTestProxy(t, upstream.URL, ProtocolHTTP)
	tp2.setFault(t, `{"mode":"duplicate_effect","nonce":"n_d"}`)
	if s, _, _ := get(tp2.front.URL); s != 200 || hits.Load() != 2 {
		t.Fatalf("status %d, server hits %d, want 2", s, hits.Load())
	}

	tp3 := newTestProxy(t, upstream.URL, ProtocolHTTP)
	tp3.setFault(t, `{"mode":"partial","keep":1,"nonce":"n_p"}`)
	if _, _, body := get(tp3.front.URL); !strings.Contains(body, `"items":[1]`) || !strings.Contains(body, "truncated") {
		t.Fatalf("partial body %s", body)
	}
}
