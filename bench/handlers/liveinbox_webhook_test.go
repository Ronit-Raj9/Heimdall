package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func webhookInfo(t *testing.T, root string, mode schemas.Mode, hosted bool, env map[string]string, host string) InboxWebhookInfo {
	t.Helper()
	h := inboxWebhookHandler{env: OrgEnv{Cfg: &lib.Config{Root: root}, Mode: mode, Services: Services{Hosted: hosted}}, getenv: func(k string) string { return env[k] }}
	r := router.New()
	r.GET(APIPrefix+"/inbox/{id}", func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(418) })
	r.GET(APIPrefix+"/inbox/webhook", h.get)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(APIPrefix + "/inbox/webhook")
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.Header.SetHost(host)
	r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var out InboxWebhookInfo
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInboxWebhookInfo(t *testing.T) {
	root := t.TempDir()
	// No webhook source: the snippet and the default variable, off.
	got := webhookInfo(t, root, schemas.ModeLive, false, nil, "bench.local:8090")
	if got.Enabled || got.Configured || got.SourcesSnippet == "" || got.TokenEnv != DefaultInboxTokenEnv {
		t.Fatalf("unconfigured: %+v", got)
	}
	if got.URL != "http://bench.local:8090/api/bench/inbox/ingest" {
		t.Fatalf("url %q", got.URL)
	}
	// A webhook source without its variable: configured, token missing.
	if err := os.MkdirAll(filepath.Join(root, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "sources:\n  - name: hook\n    kind: webhook\n    token_env: MY_INBOX_TOKEN\n"
	if err := os.WriteFile(filepath.Join(root, "inbox", "sources.yaml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got = webhookInfo(t, root, schemas.ModeLive, false, nil, "x")
	if got.Enabled || !got.Configured || got.TokenSet || got.TokenEnv != "MY_INBOX_TOKEN" || !strings.Contains(got.Next, "MY_INBOX_TOKEN") {
		t.Fatalf("no token: %+v", got)
	}
	// Token set: on, and the body never carries the value.
	got = webhookInfo(t, root, schemas.ModeLive, false, map[string]string{"MY_INBOX_TOKEN": "s3cret-value"}, "x")
	if !got.Enabled || !got.TokenSet || !strings.Contains(got.Example, "$MY_INBOX_TOKEN") {
		t.Fatalf("on: %+v", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "s3cret-value") {
		t.Fatal("token leaked")
	}
	// Hosted: off with a reason.
	if got = webhookInfo(t, root, schemas.ModeLive, true, nil, "x"); got.Enabled || got.Problem == "" || !got.Hosted {
		t.Fatalf("hosted: %+v", got)
	}
	// A crafted host header falls back to localhost.
	if got = webhookInfo(t, root, schemas.ModeLive, false, nil, "evil.com<script>"); !strings.HasPrefix(got.URL, "http://localhost/") {
		t.Fatalf("crafted host url %q", got.URL)
	}
}
