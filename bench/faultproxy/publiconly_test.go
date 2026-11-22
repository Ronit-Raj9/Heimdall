package faultproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// G3 B6: with PublicOnly an organization key's own base URL is dialed only
// at a public address, checked at connect time; off, a private one works.
func TestPublicOnlyGuardsOrgBaseURLs(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer up.Close()
	call := func(env *testEnv) int {
		t.Helper()
		slot := OrgSlot{Name: "org_a_custom_1", Provider: ProviderOpenAI, Auth: "ph-custom", Key: "ck", BaseURL: up.URL + "/v1"}
		if err := env.client.RegisterOrgSlots(context.Background(), []OrgSlot{slot}); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, env.data.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`))
		req.Header.Set("Authorization", "Bearer ph-custom")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(b), "ck") {
			t.Fatalf("answer leaks the key: %s", b)
		}
		return resp.StatusCode
	}

	off := newTestEnv(t, "", "")
	off.srv.client = up.Client()
	if code := call(off); code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("off: status %d, upstream hits %d", code, hits.Load())
	}

	on := newTestEnvConfig(t, Config{PublicOnly: true})
	on.srv.client = up.Client() // the shared client trusts the fake; the guarded one is used anyway
	if on.srv.orgClient == nil || on.srv.orgClient.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("public-only proxy has no guarded org client, or it follows an environment proxy")
	}
	// The guarded client would refuse even with the fake's certificate.
	on.srv.orgClient.Transport.(*http.Transport).TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig
	if code := call(on); code != http.StatusBadGateway || hits.Load() != 1 {
		t.Fatalf("on: status %d, upstream hits %d; want 502 and no new hit", code, hits.Load())
	}

	// The realtime relay dial is guarded the same way.
	u, _ := url.Parse(up.URL)
	if _, err := dialUpstream(context.Background(), u, true); err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("guarded realtime dial: %v", err)
	}
	plain := &url.URL{Scheme: "http", Host: u.Host} // TCP only: the fake's certificate is not trusted here
	c, err := dialUpstream(context.Background(), plain, false)
	if err != nil {
		t.Fatalf("unguarded realtime dial: %v", err)
	}
	c.Close()
}
