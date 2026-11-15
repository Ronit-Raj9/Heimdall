package toolproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestGate(t *testing.T, upstream string, token string) *Gate {
	t.Helper()
	g, err := NewGate(GateConfig{Upstream: upstream, RunID: "run-1", TokenSHA: sha256.Sum256([]byte(token)), Until: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGateAuthAndForward(t *testing.T) {
	var seen *http.Request
	var body string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, "upstream "+r.URL.Path)
	}))
	defer up.Close()
	g := newTestGate(t, up.URL, "secret-token")
	srv := httptest.NewServer(g)
	defer srv.Close()

	do := func(method, path, auth, payload string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(payload))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, _ := do("GET", "/healthz", "", ""); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
	cases := []struct {
		name, path, auth string
		want             int
	}{
		{"no token", "/r/run-1/s/notes/mcp", "", 401},
		{"wrong token", "/r/run-1/s/notes/mcp", "Bearer nope", 401},
		{"basic auth", "/r/run-1/s/notes/mcp", "Basic c2VjcmV0LXRva2Vu", 401},
		{"other run", "/r/run-2/s/notes/mcp", "Bearer secret-token", 404},
		{"no prefix", "/s/notes/mcp", "Bearer secret-token", 404},
		{"healthz of proxy", "/r/run-1/healthz", "Bearer secret-token", 404},
		{"dot segments", "/r/run-1/s/notes/../../healthz", "Bearer secret-token", 400},
		{"encoded dot segments", "/r/run-1/s/%2e%2e/x/w", "Bearer secret-token", 400},
	}
	for _, tc := range cases {
		if code, _ := do("POST", tc.path, tc.auth, "{}"); code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, code, tc.want)
		}
	}
	if seen != nil {
		t.Fatal("a refused request reached the upstream")
	}
	code, got := do("POST", "/r/run-1/s/notes/mcp", "Bearer secret-token", `{"jsonrpc":"2.0"}`)
	if code != 200 || got != "upstream /s/notes/mcp" || body != `{"jsonrpc":"2.0"}` {
		t.Fatalf("forward: %d %q body %q", code, got, body)
	}
	if seen.Header.Get("Authorization") != "" {
		t.Fatal("the token reached the tool proxy")
	}
	if code, got := do("GET", "/r/run-1/x/weather/v1/now", "Bearer secret-token", ""); code != 200 || got != "upstream /x/weather/v1/now" {
		t.Fatalf("external route: %d %q", code, got)
	}
	if code, got := do("GET", "/r/run-1/s/files/a%2Fb", "Bearer secret-token", ""); code != 200 || got != "upstream /s/files/a/b" || seen.URL.EscapedPath() != "/s/files/a%2Fb" {
		t.Fatalf("escaped path: %d %q %q", code, got, seen.URL.EscapedPath())
	}

	g.Close()
	if code, _ := do("POST", "/r/run-1/s/notes/mcp", "Bearer secret-token", "{}"); code != http.StatusGone {
		t.Fatalf("after close = %d, want 410", code)
	}
}

func TestGateDeadlineRateAndSize(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer up.Close()
	g, err := NewGate(GateConfig{Upstream: up.URL, RunID: "run-1", TokenSHA: sha256.Sum256([]byte("t")), MaxBody: 10, Rate: 1, Burst: 2})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	call := func(payload string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/r/run-1/s/a/mcp", strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer t")
		g.ServeHTTP(w, r)
		return w.Code
	}
	if c := call(strings.Repeat("x", 11)); c != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body = %d", c)
	}
	if c := call("{}"); c != 200 {
		t.Fatalf("second call = %d", c)
	}
	if c := call("{}"); c != http.StatusTooManyRequests {
		t.Fatalf("third call in the same instant = %d, want 429", c)
	}
	now = now.Add(2 * time.Second)
	if c := call("{}"); c != 200 {
		t.Fatalf("after refill = %d", c)
	}
	g.cfg.Until = now.Add(time.Second)
	now = now.Add(2 * time.Second)
	if c := call("{}"); c != http.StatusGone {
		t.Fatalf("after deadline = %d, want 410", c)
	}
}

func TestGateConfigChecks(t *testing.T) {
	sum := sha256.Sum256([]byte("t"))
	for name, cfg := range map[string]GateConfig{
		"no run":      {Upstream: "http://toolproxy:9200", TokenSHA: sum},
		"no token":    {Upstream: "http://toolproxy:9200", RunID: "r"},
		"bad url":     {Upstream: "toolproxy:9200", RunID: "r", TokenSHA: sum},
		"slash in id": {Upstream: "http://toolproxy:9200", RunID: "a/b", TokenSHA: sum},
	} {
		if _, err := NewGate(cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	got, err := ParseTokenSHA(hex.EncodeToString(sum[:]))
	if err != nil || got != sum {
		t.Fatalf("ParseTokenSHA: %v", err)
	}
	if _, err := ParseTokenSHA("abc"); err == nil {
		t.Fatal("short hash accepted")
	}
}
