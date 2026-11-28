package toolproxy

import (
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestExternalMCPAllowedPost (N115): with the allowlist a drafted MCP
// benchmark writes, a POST to the server's root and a DELETE that ends the
// session reach the server; with no allowlist the same POST is refused.
func TestExternalMCPAllowedPost(t *testing.T) {
	var seen []string
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)
	}))
	defer up.Close()
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{}}}`
	hdr := map[string]string{"Content-Type": "application/json"}

	ext := ExternalRoute{Mode: schemas.ExternalLive, MCP: true, Connect: up.Listener.Addr().String(), Allow: mcptest.DraftAllow()}
	tp := newExtProxy(t, "https://mcp.example.com/mcp", ext, roots, nil)
	if st, _, b := call(t, tp.front.URL, "POST", "/x/acme/", body, hdr); st != http.StatusOK {
		t.Fatalf("POST status %d: %s", st, b)
	}
	if st, _, b := call(t, tp.front.URL, "DELETE", "/x/acme/", "", nil); st != http.StatusOK {
		t.Fatalf("DELETE status %d: %s", st, b)
	}
	if len(seen) != 2 || seen[0] != "POST /mcp" || seen[1] != "DELETE /mcp" {
		t.Fatalf("upstream saw %v, want POST /mcp and DELETE /mcp", seen)
	}

	bare := ExternalRoute{Mode: schemas.ExternalLive, MCP: true, Connect: up.Listener.Addr().String()}
	tp2 := newExtProxy(t, "https://mcp.example.com/mcp", bare, roots, nil)
	if st, _, _ := call(t, tp2.front.URL, "POST", "/x/acme/", body, hdr); st != http.StatusForbidden {
		t.Fatalf("POST without an allowlist: status %d, want 403", st)
	}
}
