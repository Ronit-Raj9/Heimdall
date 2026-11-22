package publicingress

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type seen struct {
	path, auth, host, xff, cookie string
}

func upstream(t *testing.T, got *seen) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = seen{path: r.URL.Path, auth: r.Header.Get("Authorization"), host: r.Host, xff: r.Header.Get("X-Forwarded-For"), cookie: r.Header.Get("Cookie")}
		_, _ = io.WriteString(w, "upstream "+r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, h http.Handler, method, target string, hdr map[string]string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader("{}"))
	r.RemoteAddr = "203.0.113.7:5555"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(b)
}

func TestGatewayOnlyForLiveRunKeysOnInferenceRoutes(t *testing.T) {
	var got seen
	gw := upstream(t, &got)
	reg := NewRegistry(Limits{})
	h, err := NewHandler(reg, gw.URL)
	if err != nil {
		t.Fatal(err)
	}
	// No live run: refused, nothing forwarded.
	if code, _ := do(t, h, "POST", "/gw/v1/chat/completions", map[string]string{"Authorization": "Bearer sk-bf-live"}); code != http.StatusUnauthorized {
		t.Fatalf("no live run: %d", code)
	}
	reg.AddKey("r_1", "sk-bf-live", time.Now().Add(time.Hour))
	for _, hdr := range []map[string]string{
		{"Authorization": "Bearer sk-bf-live"},
		{"x-api-key": "sk-bf-live"},
		{"x-bf-vk": "sk-bf-live"},
		{"x-goog-api-key": "sk-bf-live"},
	} {
		hdr["Cookie"] = "session=x"
		code, body := do(t, h, "POST", "/gw/anthropic/v1/messages", hdr)
		if code != http.StatusOK || body != "upstream /anthropic/v1/messages" {
			t.Fatalf("%v: %d %q", hdr, code, body)
		}
		if got.cookie != "" || got.xff != "" {
			t.Fatalf("forwarded caller headers: %+v", got)
		}
	}
	// The admin API, metrics and the A2A gateway stay private, even with a live key.
	for _, p := range []string{"/gw/api/config", "/gw/metrics", "/gw/agents/a2a/x/json-rpc", "/gw/", "/gw/v1/../api/config"} {
		if code, _ := do(t, h, "GET", p, map[string]string{"Authorization": "Bearer sk-bf-live"}); code != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", p, code)
		}
	}
	// A dropped run's key stops working at once.
	reg.Drop("r_1")
	if code, _ := do(t, h, "POST", "/gw/v1/chat/completions", map[string]string{"Authorization": "Bearer sk-bf-live"}); code != http.StatusUnauthorized {
		t.Fatalf("dropped run: %d", code)
	}
	// An expired key too, even when the drop was missed.
	reg.AddKey("r_2", "sk-bf-old", time.Now().Add(-time.Second))
	if code, _ := do(t, h, "POST", "/gw/v1/chat/completions", map[string]string{"Authorization": "Bearer sk-bf-old"}); code != http.StatusUnauthorized {
		t.Fatalf("expired key: %d", code)
	}
}

func TestToolsOnlyWithTheRunsToken(t *testing.T) {
	var got seen
	gate := upstream(t, &got)
	u, _ := url.Parse(gate.URL)
	reg := NewRegistry(Limits{})
	h, _ := NewHandler(reg, "http://bifrost:8080")
	reg.AddGate("r_7", u.Host, sha256.Sum256([]byte("tok-7")), time.Now().Add(time.Hour))
	code, body := do(t, h, "POST", "/rt/r/r_7/s/crm/mcp", map[string]string{"Authorization": "Bearer tok-7"})
	if code != http.StatusOK || body != "upstream /r/r_7/s/crm/mcp" || got.auth != "Bearer tok-7" {
		t.Fatalf("live gate: %d %q %+v", code, body, got)
	}
	for _, c := range []struct{ path, tok string }{
		{"/rt/r/r_7/s/crm/mcp", "wrong"},
		{"/rt/r/r_7/s/crm/mcp", ""},
		{"/rt/r/r_8/s/crm/mcp", "tok-7"}, // another run's path with this run's token
	} {
		if code, _ := do(t, h, "POST", c.path, map[string]string{"Authorization": "Bearer " + c.tok}); code != http.StatusUnauthorized {
			t.Fatalf("%+v: %d", c, code)
		}
	}
	if code, _ := do(t, h, "GET", "/rt/other", nil); code != http.StatusNotFound {
		t.Fatalf("unknown path: %d", code)
	}
}

func TestRunRateLimitAndFailedAuthLimit(t *testing.T) {
	var got seen
	gw := upstream(t, &got)
	reg := NewRegistry(Limits{RunPerMinute: 60, RunBurst: 2, FailPerMinute: 3})
	now := time.Unix(1_800_000_000, 0)
	reg.now = func() time.Time { return now }
	h, _ := NewHandler(reg, gw.URL)
	reg.AddKey("r_1", "k", now.Add(time.Hour))
	auth := map[string]string{"Authorization": "Bearer k"}
	for i := 0; i < 2; i++ {
		if code, _ := do(t, h, "POST", "/gw/v1/x", auth); code != http.StatusOK {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	if code, _ := do(t, h, "POST", "/gw/v1/x", auth); code != http.StatusTooManyRequests {
		t.Fatalf("over the burst: %d", code)
	}
	now = now.Add(time.Second) // one token back at 60 a minute
	if code, _ := do(t, h, "POST", "/gw/v1/x", auth); code != http.StatusOK {
		t.Fatalf("after refill: %d", code)
	}
	// Failed authentications from one address are cut off.
	for i := 0; i < 3; i++ {
		do(t, h, "POST", "/gw/v1/x", map[string]string{"Authorization": "Bearer guess"})
	}
	if code, _ := do(t, h, "POST", "/gw/v1/x", auth); code != http.StatusTooManyRequests {
		t.Fatalf("blocked client: %d", code)
	}
}

func TestClientIPFromTrustedProxyOnly(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.2:4000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 198.51.100.4")
	if ip := h.clientIP(r); ip != "198.51.100.4" {
		t.Fatalf("behind proxy: %s", ip)
	}
	r.RemoteAddr = "203.0.113.9:4000"
	if ip := h.clientIP(r); ip != "203.0.113.9" {
		t.Fatalf("direct: %s", ip)
	}
}

func TestUpstreamErrorHidesAddresses(t *testing.T) {
	reg := NewRegistry(Limits{})
	h, _ := NewHandler(reg, "http://127.0.0.1:1")
	reg.AddKey("r_1", "k", time.Now().Add(time.Hour))
	code, body := do(t, h, "POST", "/gw/v1/x", map[string]string{"Authorization": "Bearer k"})
	if code != http.StatusBadGateway || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("%d %q", code, body)
	}
}

func TestInferenceRoute(t *testing.T) {
	for p, want := range map[string]bool{
		"/v1/chat/completions": true, "/openai/v1/responses": true, "/anthropic/v1/messages": true,
		"/genai/v1beta/models/x:generateContent": true, "/mcp": true, "/mcp/x": false,
		"/api/keys": false, "/health": false, "": false, "/v1/../api": false,
	} {
		if got := InferenceRoute(p); got != want {
			t.Errorf("%q: %t, want %t", p, got, want)
		}
	}
}
