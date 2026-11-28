package toolproxy

import (
	"bufio"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestDraftAllowStaysOnMCPServerHost checks that the allowlist a drafted MCP
// benchmark writes (path prefix "/", POST and DELETE) opens the tool proxy
// only to the drafted server: every allowed call goes to the base URL's host
// whatever Host the agent sends, and paths or request targets that name
// another host, and other external ids, never reach an upstream.
func TestDraftAllowStaysOnMCPServerHost(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.Host+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer up.Close()
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	ext := ExternalRoute{Mode: schemas.ExternalLive, MCP: true, Connect: up.Listener.Addr().String(), Allow: mcptest.DraftAllow()}
	tp := newExtProxy(t, "https://mcp.example.com/mcp", ext, roots, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	if st, _, b := call(t, tp.front.URL, "POST", "/x/acme/", body, map[string]string{"Host": "evil.example", "Content-Type": "application/json"}); st != http.StatusOK {
		t.Fatalf("POST to the drafted server: status %d: %s", st, b)
	}
	for _, p := range []string{"/x/acme//evil.example/mcp", "/x/acme/https://evil.example/mcp", "/x/acme/%2f%2fevil.example", "/x/other/"} {
		if st, _, _ := call(t, tp.front.URL, "POST", p, body, nil); st < 400 {
			t.Errorf("POST %s: status %d, want a refusal", p, st)
		}
	}
	// An absolute-form request target that names another host.
	conn, err := net.Dial("tcp", strings.TrimPrefix(tp.front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	fmt.Fprintf(conn, "POST http://evil.example/x/acme/ HTTP/1.1\r\nHost: evil.example\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("absolute-form request: status %d, want a refusal", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "POST mcp.example.com/mcp" {
		t.Fatalf("upstream saw %v, want only POST mcp.example.com/mcp", seen)
	}
}
