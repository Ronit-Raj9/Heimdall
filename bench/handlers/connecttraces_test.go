package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/agentinfer"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

const traceExport = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"support-svc"}}]},
"scopeSpans":[{"scope":{"name":"openinference.instrumentation.langchain"},"spans":[
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000001","name":"POST /chat","kind":2,"attributes":[
 {"key":"http.request.method","value":{"stringValue":"POST"}},{"key":"http.route","value":{"stringValue":"/chat"}},
 {"key":"server.address","value":{"stringValue":"support.example.com"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000002","parentSpanId":"a000000000000001","name":"invoke_agent","attributes":[
 {"key":"gen_ai.operation.name","value":{"stringValue":"invoke_agent"}},{"key":"gen_ai.agent.name","value":{"stringValue":"support-bot"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000003","parentSpanId":"a000000000000002","name":"chat","attributes":[
 {"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.request.model","value":{"stringValue":"gpt-5.4-mini"}},
 {"key":"gen_ai.input.messages","value":{"stringValue":"my card is 4111"}}]},
{"traceId":"0af7651916cd43dd8448eb211c80319c","spanId":"a000000000000004","parentSpanId":"a000000000000002","name":"execute_tool","attributes":[
 {"key":"gen_ai.operation.name","value":{"stringValue":"execute_tool"}},{"key":"gen_ai.tool.name","value":{"stringValue":"lookup_order"}}]}
]}]}]}`

type traceIngestFixture struct {
	r      *router.Router
	roots  map[string]string
	tokens inboxtoken.Store
}

func newTraceIngestFixture(t *testing.T) *traceIngestFixture {
	t.Helper()
	f := &traceIngestFixture{roots: map[string]string{"org_a": t.TempDir(), "org_b": t.TempDir()}, tokens: inboxtoken.NewMemory()}
	dir := ingestDir{"org_a": {ID: "org_a", Slug: "acme"}, "org_b": {ID: "org_b", Slug: "beta"}}
	h := NewOTLPIngestHandler(schemas.ModeLive, f.tokens, "", dir, func(_ context.Context, o tenancy.Org) (string, error) {
		return f.roots[o.ID], nil
	})
	f.r = router.New()
	h.RegisterRoutes(f.r, lib.BodyLimit(schemas.ModeLive, lib.MaxBodyBytes))
	return f
}

func (f *traceIngestFixture) token(t *testing.T, org string) string {
	t.Helper()
	_, raw, err := f.tokens.Create(context.Background(), org, "otel", "u1")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func otlpPost(r *router.Router, path, bearer, ct, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.Header.SetHost("heimdall.example")
	ctx.Request.Header.SetContentType(ct)
	if bearer != "" {
		ctx.Request.Header.Set("Authorization", "Bearer "+bearer)
	}
	ctx.Request.SetBodyString(body)
	r.Handler(ctx)
	return ctx
}

func TestOTLPOrgIngest(t *testing.T) {
	f := newTraceIngestFixture(t)
	tokA, tokB := f.token(t, "org_a"), f.token(t, "org_b")
	url := OTLPOrgIngestURL("acme")
	for name, bearer := range map[string]string{"none": "", "wrong": "hmi_nottherighttokenatall", "other org": tokB} {
		if ctx := otlpPost(f.r, url, bearer, "application/json", traceExport); ctx.Response.StatusCode() != 401 {
			t.Fatalf("%s: %d %s", name, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	if ctx := otlpPost(f.r, OTLPOrgIngestURL("nope"), tokA, "application/json", traceExport); ctx.Response.StatusCode() != 401 {
		t.Fatalf("unknown org: %d", ctx.Response.StatusCode())
	}
	ctx := otlpPost(f.r, url, tokA, "application/json", traceExport)
	if ctx.Response.StatusCode() != 200 || !strings.Contains(string(ctx.Response.Body()), `"accepted":4`) {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	// A header copied with %20 still encoded is accepted.
	ctx = otlpPost(f.r, OTLPOrgIngestURL("org_a"), strings.Replace(tokA, "_", "%5F", 1), "application/json", traceExport)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("encoded token: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	// Protobuf: an empty protobuf answer.
	ctx = otlpPost(f.r, url, tokA, "application/x-protobuf", "")
	if ctx.Response.StatusCode() != 200 || string(ctx.Response.Header.ContentType()) != "application/x-protobuf" || len(ctx.Response.Body()) != 0 {
		t.Fatalf("proto: %d %q %q", ctx.Response.StatusCode(), ctx.Response.Header.ContentType(), ctx.Response.Body())
	}
	if ctx := otlpPost(f.r, url, tokA, "application/json", `{"x":1}`); ctx.Response.StatusCode() != 400 {
		t.Fatalf("bad body: %d", ctx.Response.StatusCode())
	}
	st, err := agentinfer.NewStore(f.roots["org_a"]).Load()
	if err != nil || len(st.Spans) != 8 || st.Exports != 2 {
		t.Fatalf("org_a store: %v %d %d", err, len(st.Spans), st.Exports)
	}
	if st, _ := agentinfer.NewStore(f.roots["org_b"]).Load(); len(st.Spans) != 0 {
		t.Fatal("org_b got org_a's spans")
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "4111") {
		t.Fatal("prompt text kept")
	}
}

func TestOTLPIngestBodyLimitAndRate(t *testing.T) {
	f := newTraceIngestFixture(t)
	tok := f.token(t, "org_a")
	big := strings.Repeat(" ", 2<<20) + traceExport // over the 1 MiB platform default, under 4 MiB
	if ctx := otlpPost(f.r, OTLPOrgIngestURL("org_a"), tok, "application/json", big); ctx.Response.StatusCode() != 200 {
		t.Fatalf("2 MiB export: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if ctx := otlpPost(f.r, OTLPOrgIngestURL("org_a"), tok, "application/json", strings.Repeat(" ", 5<<20)); ctx.Response.StatusCode() != 413 {
		t.Fatalf("5 MiB export: %d", ctx.Response.StatusCode())
	}
}

func connectTracesRouter(t *testing.T, root string, hosted bool) *router.Router {
	t.Helper()
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: schemas.ModeLive, Services: Services{OrgID: "org_a", Hosted: hosted}}
	h := &connectTracesHandler{env: env}
	r := router.New()
	r.GET(ConnectTracesPath, env.R(h.get))
	r.DELETE(ConnectTracesPath, env.W(h.forget))
	r.POST(ConnectTracesInferPath, env.U(h.infer))
	r.POST(AgentFileCheckPath, env.W(h.checkFile))
	r.POST(OTLPLocalIngestPath, env.U(h.localIngest))
	return r
}

func getTraces(t *testing.T, r *router.Router) TraceConnectInfo {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(ConnectTracesPath)
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.Header.SetHost("heimdall.example")
	r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var info TraceConnectInfo
	if err := json.Unmarshal(ctx.Response.Body(), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestConnectTracesDraft(t *testing.T) {
	root := t.TempDir()
	r := connectTracesRouter(t, root, true)
	info := getTraces(t, r)
	if info.Draft != nil || info.Endpoint != "http://heimdall.example"+OTLPOrgIngestURL("org_a") || info.Base != "http://heimdall.example/api/bench/orgs/org_a/otlp" || len(info.Setup) != 4 {
		t.Fatalf("%+v", info)
	}
	if !strings.Contains(info.Setup[0].Env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="+info.Endpoint) || !strings.Contains(info.Setup[2].Env, "TRACELOOP_BASE_URL="+info.Base) {
		t.Fatalf("setup %+v", info.Setup)
	}
	if _, err := agentinfer.NewStore(root).Add(mustSpans(t), fasthttpNow()); err != nil {
		t.Fatal(err)
	}
	info = getTraces(t, r)
	if info.Draft == nil || info.Draft.Name != "support-bot" || info.Request == nil || info.Request.Template != "http-url" ||
		info.Request.Params["url"] != "https://support.example.com/chat" || info.Request.Model.Model != "gpt-5.4-mini" {
		t.Fatalf("%+v %+v", info.Draft, info.Request)
	}
	if !strings.Contains(info.AgentYAML, "template: http-url") || !strings.Contains(info.Summary, "tools seen: lookup_order") || info.SpansKept != 4 {
		t.Fatalf("%s / %s", info.AgentYAML, info.Summary)
	}
	// The draft's request renders as a valid agent.
	hreq := AgentFromTemplateRequest{Template: info.Request.Template, ID: info.Request.ID, Params: info.Request.Params,
		Model: &AgentModelRef{Provider: info.Request.Model.Provider, Model: info.Request.Model.Model}}
	files, err := RenderAgentRequest(hreq)
	if err != nil || !strings.Contains(string(files[0].Data), "https://support.example.com/chat") {
		t.Fatalf("render: %v", err)
	}

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(ConnectTracesPath)
	ctx.Request.Header.SetMethod("DELETE")
	r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 || getTraces(t, r).Draft != nil {
		t.Fatalf("forget: %d", ctx.Response.StatusCode())
	}
}

func TestConnectTracesInferAndLocalIngest(t *testing.T) {
	root := t.TempDir()
	r := connectTracesRouter(t, root, false)
	ctx := otlpPost(r, ConnectTracesInferPath, "", "application/json", traceExport)
	var res TraceInferResult
	if ctx.Response.StatusCode() != 200 || json.Unmarshal(ctx.Response.Body(), &res) != nil || res.Draft.Name != "support-bot" || res.Request == nil {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if st, _ := agentinfer.NewStore(root).Load(); len(st.Spans) != 0 {
		t.Fatal("infer kept spans")
	}
	if ctx := otlpPost(r, ConnectTracesInferPath, "", "application/json", "nope"); ctx.Response.StatusCode() != 400 {
		t.Fatalf("bad export: %d", ctx.Response.StatusCode())
	}
	if ctx := otlpPost(r, OTLPLocalIngestPath, "", "application/json", traceExport); ctx.Response.StatusCode() != 200 {
		t.Fatalf("local ingest: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	info := getTraces(t, r)
	if info.Hosted || info.Endpoint != "http://heimdall.example"+OTLPLocalIngestPath || info.Draft == nil || info.Exports != 1 {
		t.Fatalf("%+v", info)
	}
}

func TestAgentFileCheckRoute(t *testing.T) {
	r := connectTracesRouter(t, t.TempDir(), true)
	body, _ := json.Marshal(AgentFileCheckRequest{YAML: "id: bot\nconnect: {template: http-url, url: 'https://bot.example.com/run', auth_secret: BOT_TOKEN}\n"})
	ctx := otlpPost(r, AgentFileCheckPath, "", "application/json", string(body))
	var res AgentFileCheckResult
	if json.Unmarshal(ctx.Response.Body(), &res) != nil || !res.OK || res.Request.Params["auth_secret"] != "BOT_TOKEN" {
		t.Fatalf("%s", ctx.Response.Body())
	}
	body, _ = json.Marshal(AgentFileCheckRequest{YAML: "id: bot\nconnect: {template: http-url, api_key: abc}\n"})
	ctx = otlpPost(r, AgentFileCheckPath, "", "application/json", string(body))
	res = AgentFileCheckResult{}
	if json.Unmarshal(ctx.Response.Body(), &res) != nil || res.OK || !strings.Contains(res.Error, "never holds them") {
		t.Fatalf("%s", ctx.Response.Body())
	}
}

func mustSpans(t *testing.T) []agentevents.Span {
	t.Helper()
	sp, err := agentevents.DecodeSpans([]byte(traceExport), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func fasthttpNow() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) }
