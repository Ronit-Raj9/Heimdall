package toolproxy

import (
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestExternalMCPRoute: an MCP external route sends a root call to the MCP
// endpoint itself, answers GET 405 without reaching the server, names the
// tool on the ledger row and flags isError and JSON-RPC errors as errors.
func TestExternalMCPRoute(t *testing.T) {
	var paths []string
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		paths = append(paths, r.Method+" "+r.URL.Path)
		body := string(b)
		switch {
		case strings.Contains(body, `"broken"`):
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"content\":[],\"isError\":true}}\n\n")
		case strings.Contains(body, `"nope/method"`):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":3,"error":{"code":-32601,"message":"Method not found"}}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)
		}
	}))
	defer up.Close()
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	ext := ExternalRoute{Mode: schemas.ExternalLive, MCP: true, Connect: up.Listener.Addr().String(),
		Allow: []schemas.ExternalAllow{{Methods: []string{"POST", "DELETE"}, PathPrefix: "/"}}, AuthEnv: "ACME_KEY", AuthScheme: "Bearer"}
	tp := newExtProxy(t, "https://mcp.example.com/mcp", ext, roots, nil)

	if st, _, b := call(t, tp.front.URL, "POST", "/x/acme", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"q":"x"}}}`, map[string]string{"Content-Type": "application/json"}); st != 200 {
		t.Fatalf("call status %d: %s", st, b)
	}
	call(t, tp.front.URL, "POST", "/x/acme", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"broken","arguments":{}}}`, nil)
	call(t, tp.front.URL, "POST", "/x/acme", `{"jsonrpc":"2.0","id":3,"method":"nope/method"}`, nil)
	st, hdr, _ := call(t, tp.front.URL, "GET", "/x/acme", "", nil)
	if st != http.StatusMethodNotAllowed || hdr.Get("Allow") == "" {
		t.Fatalf("GET status %d allow %q", st, hdr.Get("Allow"))
	}
	if len(paths) != 3 || paths[0] != "POST /mcp" {
		t.Fatalf("upstream saw %v, want 3 POST /mcp", paths)
	}
	rows := tp.rows(t)
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	want := []struct {
		tool  string
		isErr bool
	}{{"search", false}, {"broken", true}, {"nope/method", true}, {"GET /", true}}
	for i, w := range want {
		if rows[i].Tool != w.tool || rows[i].IsError != w.isErr {
			t.Fatalf("row %d = tool %q error %v, want %q %v", i, rows[i].Tool, rows[i].IsError, w.tool, w.isErr)
		}
	}
	assertNoSecret(t, tp.ledger)
}

func TestMCPRequestLabel(t *testing.T) {
	cases := map[string]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`:                       "initialize",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a"}}`: "a",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"bad"}`:        "tools/call",
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`:                           "batch",
		`not json`: "POST /",
	}
	for body, want := range cases {
		if got := mcpRequestLabel([]byte(body), "POST /"); got != want {
			t.Errorf("label(%s) = %q, want %q", body, got, want)
		}
	}
}
