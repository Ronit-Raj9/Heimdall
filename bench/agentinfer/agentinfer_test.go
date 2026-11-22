package agentinfer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
)

type tspan struct {
	trace, id, parent, name string
	kind                    int
	attrs                   map[string]string
	errStatus               bool
}

// otlpJSON builds an OTLP/JSON export with one resource and one scope.
func otlpJSON(service, scope string, spans ...tspan) []byte {
	var ss []map[string]any
	for i, s := range spans {
		var attrs []map[string]any
		for k, v := range s.attrs {
			attrs = append(attrs, map[string]any{"key": k, "value": map[string]any{"stringValue": v}})
		}
		m := map[string]any{"traceId": s.trace, "spanId": s.id, "name": s.name, "attributes": attrs,
			"startTimeUnixNano": fmt.Sprint(time.Date(2026, 11, 1, 10, 0, i, 0, time.UTC).UnixNano()),
			"endTimeUnixNano":   fmt.Sprint(time.Date(2026, 11, 1, 10, 0, i+1, 0, time.UTC).UnixNano())}
		if s.parent != "" {
			m["parentSpanId"] = s.parent
		}
		if s.kind != 0 {
			m["kind"] = s.kind
		}
		if s.errStatus {
			m["status"] = map[string]any{"code": 2}
		}
		ss = append(ss, m)
	}
	body := map[string]any{"resourceSpans": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{
			map[string]any{"key": "service.name", "value": map[string]any{"stringValue": service}},
			map[string]any{"key": "telemetry.sdk.language", "value": map[string]any{"stringValue": "python"}},
		}},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": scope, "version": "0.1"}, "spans": ss}},
	}}}
	b, _ := json.Marshal(body)
	return b
}

const t1, t2 = "0af7651916cd43dd8448eb211c80319c", "1af7651916cd43dd8448eb211c80319c"

func supportBot(trace, conv string) []tspan {
	return []tspan{
		{trace: trace, id: "a000000000000001", name: "POST /chat", kind: 2, attrs: map[string]string{
			"http.request.method": "POST", "http.route": "/chat", "server.address": "support.example.com", "url.scheme": "https"}},
		{trace: trace, id: "a000000000000002", parent: "a000000000000001", name: "invoke_agent support-bot", attrs: map[string]string{
			"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "support-bot", "gen_ai.conversation.id": conv}},
		{trace: trace, id: "a000000000000003", parent: "a000000000000002", name: "chat gpt-5.4-mini", attrs: map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.request.model": "gpt-5.4-mini", "gen_ai.provider.name": "openai",
			"gen_ai.system_instructions": "You are a support bot", "gen_ai.input.messages": "secret customer text"}},
		{trace: trace, id: "a000000000000004", parent: "a000000000000002", name: "execute_tool lookup_order", attrs: map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "lookup_order", "gen_ai.tool.call.arguments": `{"order":"A-1"}`}},
		{trace: trace, id: "a000000000000005", parent: "a000000000000002", name: "execute_tool refund", errStatus: true, attrs: map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "refund"}},
		{trace: trace, id: "a000000000000006", parent: "a000000000000001", name: "POST api.openai.com", kind: 3, attrs: map[string]string{
			"http.request.method": "POST", "url.full": "https://api.openai.com/v1/chat/completions"}},
	}
}

func TestInferSupportBot(t *testing.T) {
	body := otlpJSON("support-svc", "openinference.instrumentation.langchain", append(supportBot(t1, "c-1"), supportBot(t2, "c-1")...)...)
	d, err := InferFromOTLP(body, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "support-bot" || d.ID != "support-bot" || d.Service != "support-svc" || d.Framework != "langchain" || d.Language != "python" {
		t.Fatalf("identity: %+v", d)
	}
	if len(d.Models) != 1 || d.Models[0].Value != "gpt-5.4-mini" || d.Provider != "openai" {
		t.Fatalf("model: %+v %s", d.Models, d.Provider)
	}
	if len(d.Tools) != 2 || d.Tools[0].Calls != 2 || d.Tools[1].Name != "refund" && d.Tools[0].Name != "refund" {
		t.Fatalf("tools: %+v", d.Tools)
	}
	var refund ToolSeen
	for _, tl := range d.Tools {
		if tl.Name == "refund" {
			refund = tl
		}
	}
	if refund.Errors != 2 {
		t.Fatalf("refund errors: %+v", refund)
	}
	if d.Route == nil || d.Route.URL != "https://support.example.com/chat" || d.Route.Private || d.Route.Count != 2 {
		t.Fatalf("route: %+v", d.Route)
	}
	if !d.MultiTurn || d.Conversations != 1 || !d.SystemInstructionsSeen || d.Traces != 2 {
		t.Fatalf("state: %+v", d)
	}
	if d.Template != TemplateHTTP || d.Params["url"] != "https://support.example.com/chat" || d.Model == nil || d.Model.Model != "gpt-5.4-mini" {
		t.Fatalf("proposal: %+v", d)
	}
	if d.Confidence != ConfidenceHigh {
		t.Fatalf("confidence %s", d.Confidence)
	}
	if strings.Join(d.Capabilities, ",") != "tools,sessions" {
		t.Fatalf("caps %v", d.Capabilities)
	}
	if got := d.Summary(); !strings.Contains(got, "support-bot, langchain, gpt-5.4-mini, tools seen: lookup_order, refund, served at POST /chat") {
		t.Fatalf("summary %q", got)
	}
}

func TestInferPrivateRouteAndNoRoute(t *testing.T) {
	spans := []tspan{
		{trace: t1, id: "b000000000000001", name: "POST /runs/wait", kind: 2, attrs: map[string]string{
			"http.request.method": "POST", "url.path": "/threads/3f2b1c4d-1111-2222-3333-444455556666/runs/wait", "server.address": "10.0.3.4", "server.port": "2024"}},
		{trace: t1, id: "b000000000000002", parent: "b000000000000001", name: "LangGraph", attrs: map[string]string{"openinference.span.kind": "AGENT"}},
		{trace: t1, id: "b000000000000003", parent: "b000000000000002", name: "ChatAnthropic", attrs: map[string]string{
			"openinference.span.kind": "LLM", "llm.model_name": "claude-sonnet-4-5"}},
	}
	d := InferAgent(mustDecode(t, otlpJSON("graph", "openinference.instrumentation.langchain", spans...)))
	if d.Framework != "langgraph" || d.Provider != "anthropic" || d.Name != "graph" {
		t.Fatalf("%+v", d)
	}
	if d.Route == nil || !d.Route.Private || d.Route.Path != "/threads/{id}/runs/wait" || d.Route.Host != "10.0.3.4:2024" || d.Route.Scheme != "http" {
		t.Fatalf("route %+v", d.Route)
	}
	if d.Template != "" || d.Preset != "langgraph" || d.Confidence != ConfidenceMedium || !strings.Contains(strings.Join(d.Questions, " "), "heimdall serve") {
		t.Fatalf("proposal %+v", d)
	}

	// No server span: a wrap-app hint for a known framework.
	d = InferAgent(mustDecode(t, otlpJSON("crew", "openinference.instrumentation.crewai", tspan{trace: t1, id: "c000000000000001", name: "Crew.kickoff",
		attrs: map[string]string{"openinference.span.kind": "AGENT", "llm.model_name": "gpt-5.4"}})))
	if d.Framework != "crewai" || d.Template != TemplateWrapApp || d.Params["framework"] != "crewai" || d.Route != nil {
		t.Fatalf("%+v", d)
	}

	// Nothing useful.
	d = InferAgent(nil)
	if d.Confidence != ConfidenceLow || d.ID != "traced-agent" || len(d.Questions) == 0 {
		t.Fatalf("%+v", d)
	}
}

func TestInferOpenAIAndA2ARoutes(t *testing.T) {
	d := InferAgent(mustDecode(t, otlpJSON("svc", "x", tspan{trace: t1, id: "d000000000000001", name: "POST", kind: 2, attrs: map[string]string{
		"http.method": "POST", "http.target": "/v1/chat/completions?x=1", "http.host": "agent.example.com"}})))
	if d.Template != TemplateOpenAI || d.Params["url"] != "https://agent.example.com/v1/chat/completions" || d.Preset != "chat_completions" {
		t.Fatalf("%+v %+v", d, d.Route)
	}
	d = InferAgent(mustDecode(t, otlpJSON("svc", "x", tspan{trace: t1, id: "d000000000000002", name: "GET", kind: 2, attrs: map[string]string{
		"http.request.method": "GET", "url.full": "https://a2a.example.com/.well-known/agent-card.json"}})))
	if d.Template != TemplateA2A || d.Params["card_url"] != "https://a2a.example.com/.well-known/agent-card.json" {
		t.Fatalf("%+v %+v", d, d.Route)
	}
	// Health checks are not the agent route.
	d = InferAgent(mustDecode(t, otlpJSON("svc", "x", tspan{trace: t1, id: "d000000000000003", name: "GET /health", kind: 2, attrs: map[string]string{
		"http.request.method": "GET", "http.route": "/health", "server.address": "agent.example.com"}})))
	if d.Route != nil {
		t.Fatalf("health route kept: %+v", d.Route)
	}
}

func TestDeclaredToolsAndSubagents(t *testing.T) {
	defs := `[{"type":"function","name":"search"},{"type":"function","function":{"name":"fetch"}}]`
	spans := []tspan{
		{trace: t1, id: "e000000000000001", name: "invoke_agent planner", attrs: map[string]string{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "planner"}},
		{trace: t1, id: "e000000000000002", parent: "e000000000000001", name: "invoke_agent researcher", attrs: map[string]string{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "researcher"}},
		{trace: t1, id: "e000000000000003", parent: "e000000000000002", name: "chat", attrs: map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.request.model": "gemini-2.5-pro", "gen_ai.tool.definitions": defs,
			"llm.tools.0.tool.json_schema": `{"name":"browse"}`}},
		{trace: t1, id: "e000000000000004", parent: "e000000000000002", name: "retrieve docs", attrs: map[string]string{"openinference.span.kind": "RETRIEVER"}},
	}
	d := InferAgent(mustDecode(t, otlpJSON("svc", "openai_agents", spans...)))
	if d.Name != "planner" || strings.Join(d.Subagents, ",") != "researcher" || d.Framework != "openai-agents" || d.Provider != "gemini" {
		t.Fatalf("%+v", d)
	}
	if strings.Join(d.ToolsDeclared, ",") != "browse,fetch,search" || d.Retrievals != 1 || !strings.Contains(strings.Join(d.Packs, ","), "research") {
		t.Fatalf("%+v", d)
	}
}

func mustDecode(t *testing.T, body []byte) []agentevents.Span {
	t.Helper()
	sp, err := agentevents.DecodeSpans(body, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestStoreCompactsAndCaps(t *testing.T) {
	s := Store{Dir: t.TempDir(), Max: 8}
	spans := mustDecode(t, otlpJSON("support-svc", "openinference.instrumentation.langchain", supportBot(t1, "c-1")...))
	if _, err := s.Add(spans, time.Now()); err != nil {
		t.Fatal(err)
	}
	st, err := s.Add(spans, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Spans) != 8 || st.Dropped != 4 || st.Exports != 2 {
		t.Fatalf("cap: %d %d %d", len(st.Spans), st.Dropped, st.Exports)
	}
	st, _ = s.Load()
	raw, _ := json.Marshal(st)
	for _, leak := range []string{"secret customer text", "You are a support bot", `A-1`} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("store kept %q", leak)
		}
	}
	d := InferAgent(st.Spans)
	if d.Name != "support-bot" || !d.SystemInstructionsSeen || d.Route == nil || d.Route.URL != "https://support.example.com/chat" {
		t.Fatalf("inference after compaction: %+v", d)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Load(); len(st.Spans) != 0 {
		t.Fatal("clear kept spans")
	}
}
