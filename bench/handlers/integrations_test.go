package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

func integCtx(method, body string, params map[string]string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetBodyString(body)
	for k, v := range params {
		ctx.SetUserValue(k, v)
	}
	return ctx
}

func TestIntegrationsWebhookSecretShownOnce(t *testing.T) {
	h := &integrationsHandler{mode: "live", root: t.TempDir(), hosted: true}
	ctx := integCtx("POST", `{"url":"https://example.com/h"}`, nil)
	h.createHook(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("create %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var created WebhookCreated
	_ = json.Unmarshal(ctx.Response.Body(), &created)
	if !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("no secret: %s", ctx.Response.Body())
	}
	ctx = integCtx("GET", "", nil)
	h.listHooks(ctx)
	if strings.Contains(string(ctx.Response.Body()), created.Secret) {
		t.Fatal("list leaks the secret")
	}
	// Hosted refuses a loopback receiver.
	ctx = integCtx("POST", `{"url":"https://127.0.0.1/h"}`, nil)
	h.createHook(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("loopback accepted: %d", ctx.Response.StatusCode())
	}
}

func TestIntegrationsExportConfined(t *testing.T) {
	root := t.TempDir()
	dir := IntegrationsExportDir(root)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "e1-x.runs.csv"), []byte("a,b\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "secret.runs.csv"), []byte("no"), 0o644)
	h := &integrationsHandler{mode: "live", root: root}
	ctx := integCtx("GET", "", map[string]string{"name": "e1-x.runs.csv"})
	h.downloadExport(ctx)
	if ctx.Response.StatusCode() != 200 || string(ctx.Response.Body()) != "a,b\n" {
		t.Fatalf("download %d", ctx.Response.StatusCode())
	}
	for _, bad := range []string{"../secret.runs.csv", "..", ".hidden.runs.csv", "e1-x.txt"} {
		ctx = integCtx("GET", "", map[string]string{"name": bad})
		h.downloadExport(ctx)
		if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Errorf("%s served: %d", bad, ctx.Response.StatusCode())
		}
	}
	xs, _ := ListIntegrationExports(root)
	if len(xs) != 1 || xs[0].Format != "csv" {
		t.Fatalf("list %+v", xs)
	}
}

func TestIntegrationsCIWorkflowPreview(t *testing.T) {
	h := &integrationsHandler{mode: "live"}
	ctx := integCtx("GET", "", nil)
	h.ciWorkflow(ctx)
	if ctx.Response.StatusCode() != 200 || !strings.Contains(string(ctx.Response.Body()), "heimdall.yml") {
		t.Fatalf("preview %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestIntegrationsCIHostedWorkflow(t *testing.T) {
	h := &integrationsHandler{mode: "live"}
	ctx := integCtx("GET", "", nil)
	ctx.QueryArgs().Set("mode", "hosted")
	ctx.QueryArgs().Set("agent", "my-agent")
	ctx.QueryArgs().Set("pack", "coding")
	ctx.QueryArgs().Set("faults", "model-down,broken-tool")
	ctx.QueryArgs().Set("url", "https://heimdall.example.com")
	h.ciWorkflow(ctx)
	body := string(ctx.Response.Body())
	if ctx.Response.StatusCode() != 200 || !strings.Contains(body, "heimdall-evaluate.yml") || !strings.Contains(body, `agents/my-agent.yaml`) || !strings.Contains(body, "model-down,broken-tool") {
		t.Fatalf("github %d %s", ctx.Response.StatusCode(), body)
	}
	ctx = integCtx("GET", "", nil)
	ctx.QueryArgs().Set("mode", "hosted")
	ctx.QueryArgs().Set("provider", "gitlab")
	ctx.QueryArgs().Set("agent", "my-agent")
	ctx.QueryArgs().Set("benchmarks", "hd-harbor-tb2")
	h.ciWorkflow(ctx)
	if ctx.Response.StatusCode() != 200 || !strings.Contains(string(ctx.Response.Body()), "merge_request_event") {
		t.Fatalf("gitlab %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = integCtx("GET", "", nil)
	ctx.QueryArgs().Set("mode", "hosted")
	ctx.QueryArgs().Set("agent", "my-agent")
	h.ciWorkflow(ctx)
	if ctx.Response.StatusCode() != 400 {
		t.Fatalf("no benchmarks: %d", ctx.Response.StatusCode())
	}
}
