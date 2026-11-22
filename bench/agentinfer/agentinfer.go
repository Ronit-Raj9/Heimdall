// Package agentinfer proposes a Heimdall agent from what is already known
// about it: the OpenTelemetry traces it sends (InferAgent, InferFromOTLP)
// and a committed .heimdall/agent.yaml (agentfile.go). The result is a
// draft: a connect template, its parameters and a model, ready for
// POST /agents/from-template, plus the evidence behind each field.
//
// Trace inference proposes and never decides (INFRA_I2 3.4): GenAI agent
// spans are still in development and most of what an agent is (its full
// tool list, its system prompt, its auth) is opt-in or absent from a trace.
// Every field is shown as "seen in your traces" and confirmed by the
// connect test before it counts.
package agentinfer

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
)

// Confidence of a draft.
const (
	ConfidenceHigh   = "high"   // an agent, a model and a public serving route were seen
	ConfidenceMedium = "medium" // an agent and a model, but no usable route
	ConfidenceLow    = "low"    // little more than LLM calls
)

// Connect templates a draft can propose (handlers/agentbuilder.go).
const (
	TemplateHTTP    = "http-url"
	TemplateOpenAI  = "openai-endpoint"
	TemplateA2A     = "a2a-card"
	TemplateWrapApp = "wrap-app"
)

// ModelRef is the model a draft proposes, as POST /agents/from-template
// takes it.
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Seen is a value and how many spans carried it.
type Seen struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// ToolSeen is one tool the agent called in the traces.
type ToolSeen struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors,omitempty"`
}

// Route is the HTTP route the agent was served on, from HTTP server spans.
type Route struct {
	Method string `json:"method"`
	Scheme string `json:"scheme,omitempty"`
	Host   string `json:"host,omitempty"` // host[:port]; empty when the spans do not say
	Path   string `json:"path"`
	// URL is scheme://host/path when the host is known.
	URL string `json:"url,omitempty"`
	// Private is set when the host is loopback, private or a bare cluster
	// name: the shared worker never calls it, so it is not proposed as the
	// endpoint URL.
	Private bool `json:"private,omitempty"`
	Count   int  `json:"count"`
}

// Evidence says where one proposed field came from.
type Evidence struct {
	Field string `json:"field"`
	Value string `json:"value"`
	From  string `json:"from"` // the span attribute or rule
}

// Draft is an agent proposed from traces or an agent file.
type Draft struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Service   string `json:"service,omitempty"`
	Framework string `json:"framework,omitempty"` // langgraph, crewai, openai-agents, adk, ...
	Language  string `json:"language,omitempty"`

	Models   []Seen `json:"models,omitempty"`
	Provider string `json:"provider,omitempty"`

	Tools         []ToolSeen `json:"tools,omitempty"`          // tools called in the traces
	ToolsDeclared []string   `json:"tools_declared,omitempty"` // from gen_ai.tool.definitions or llm.tools, when sent
	Retrievals    int        `json:"retrievals,omitempty"`
	MemoryOps     int        `json:"memory_ops,omitempty"`
	Subagents     []string   `json:"subagents,omitempty"`

	Conversations int `json:"conversations,omitempty"`
	// MultiTurn is set when one conversation id spans several traces: the
	// agent keeps state across turns.
	MultiTurn              bool `json:"multi_turn,omitempty"`
	SystemInstructionsSeen bool `json:"system_instructions_seen,omitempty"`

	Route *Route `json:"route,omitempty"`

	Traces    int       `json:"traces"`
	Spans     int       `json:"spans"`
	LLMCalls  int       `json:"llm_calls,omitempty"`
	FirstSeen time.Time `json:"first_seen,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`

	// Capabilities are capability names (schemas/capabilities.go) the
	// traces suggest: tools, sessions. Hints only.
	Capabilities []string `json:"capabilities,omitempty"`
	// Packs are the benchmark packs the traces point at (support,
	// research, memory). Hints only.
	Packs []string `json:"packs,omitempty"`

	// Template, Params and Model are the proposal for
	// POST /agents/from-template; Template is empty when nothing in the
	// traces says how to reach the agent.
	Template string            `json:"template,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Model    *ModelRef         `json:"model,omitempty"`
	// Preset is the endpoint platform preset (lane Y10 names: langgraph,
	// adk, mastra, openai_responses, chat_completions, custom).
	Preset string `json:"preset,omitempty"`

	Confidence string     `json:"confidence"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	// Questions are what the person still has to answer before the agent
	// can be connected.
	Questions []string `json:"questions,omitempty"`
}

// InferFromOTLP decodes an OTLP/HTTP trace export (protobuf or JSON, by the
// content type) and infers the agent.
func InferFromOTLP(body []byte, contentType string) (Draft, error) {
	spans, err := agentevents.DecodeSpans(body, contentType)
	if err != nil {
		return Draft{}, err
	}
	return InferAgent(spans), nil
}

// counter counts values and keeps the first-seen order for ties.
type counter struct {
	n     map[string]int
	order []string
}

func (c *counter) add(v string) {
	if v == "" {
		return
	}
	if c.n == nil {
		c.n = map[string]int{}
	}
	if _, ok := c.n[v]; !ok {
		c.order = append(c.order, v)
	}
	c.n[v]++
}

func (c *counter) sorted() []Seen {
	out := make([]Seen, 0, len(c.order))
	for _, v := range c.order {
		out = append(out, Seen{Value: v, Count: c.n[v]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func (c *counter) top() string {
	s := c.sorted()
	if len(s) == 0 {
		return ""
	}
	return s[0].Value
}

// InferAgent reads spans and proposes an agent. It never fails: with no
// usable span it returns a low-confidence draft with questions.
func InferAgent(spans []agentevents.Span) Draft {
	d := Draft{Spans: len(spans)}
	var agents, services, models, providers, frameworks, languages, routes counter
	var subagents counter
	tools := map[string]*ToolSeen{}
	var toolOrder []string
	declared := map[string]bool{}
	traces := map[string]bool{}
	convTraces := map[string]map[string]bool{}
	routeInfo := map[string]Route{}
	frameworkFrom := ""
	agentSpanIDs := map[string]bool{}
	for _, sp := range spans {
		if sp.Type == agentevents.TypeAgent {
			agentSpanIDs[sp.TraceID+"/"+sp.SpanID] = true
		}
	}
	for _, sp := range spans {
		a := sp.Attributes
		traces[sp.TraceID] = true
		if !sp.Start.IsZero() && (d.FirstSeen.IsZero() || sp.Start.Before(d.FirstSeen)) {
			d.FirstSeen = sp.Start
		}
		if t := latest(sp); !t.IsZero() && t.After(d.LastSeen) {
			d.LastSeen = t
		}
		services.add(sp.Service)
		if lang := sp.Resource["telemetry.sdk.language"]; lang != "" {
			languages.add(lang)
		}
		if fw, why := frameworkOf(sp); fw != "" {
			frameworks.add(fw)
			if frameworkFrom == "" {
				frameworkFrom = why
			}
		}
		if p := providerOf(a); p != "" {
			providers.add(p)
		}
		switch sp.Type {
		case agentevents.TypeAgent:
			name := first(a, "gen_ai.agent.name", "agent.name")
			if sp.ParentID != "" && agentSpanIDs[sp.TraceID+"/"+sp.ParentID] {
				subagents.add(name)
			} else {
				agents.add(name)
			}
		case agentevents.TypeLLM:
			d.LLMCalls++
			models.add(first(a, "gen_ai.request.model", "gen_ai.response.model", "llm.model_name"))
		case agentevents.TypeToolCall:
			name := first(a, "gen_ai.tool.name", "tool.name", "tool_name")
			if name == "" {
				name = sp.Name
			}
			t, ok := tools[name]
			if !ok {
				t = &ToolSeen{Name: name}
				tools[name] = t
				toolOrder = append(toolOrder, name)
			}
			t.Calls++
			if sp.Error {
				t.Errors++
			}
		case agentevents.TypeRetrieval:
			d.Retrievals++
		case agentevents.TypeMemoryRead, agentevents.TypeMemoryWrite:
			d.MemoryOps++
		}
		if sp.Type != agentevents.TypeLLM {
			if m := first(a, "gen_ai.request.model", "llm.model_name"); m != "" && sp.Type == agentevents.TypeAgent {
				models.add(m)
			}
		}
		for _, n := range declaredTools(a) {
			declared[n] = true
		}
		if a["gen_ai.system_instructions"] != "" || a["heimdall.system_instructions_seen"] != "" {
			d.SystemInstructionsSeen = true
		}
		if c := first(a, "gen_ai.conversation.id", "session.id", "conversation.id"); c != "" {
			if convTraces[c] == nil {
				convTraces[c] = map[string]bool{}
			}
			convTraces[c][sp.TraceID] = true
		}
		if r, ok := routeOf(sp); ok {
			k := r.Method + " " + r.Host + r.Path
			routes.add(k)
			routeInfo[k] = r
		}
	}
	d.Traces = len(traces)
	d.Service = services.top()
	d.Name = agents.top()
	d.Framework = frameworks.top()
	if d.Framework == "langchain" && frameworks.n["langgraph"] > 0 { // LangGraph runs on LangChain's instrumentation
		d.Framework = "langgraph"
	}
	d.Language = languages.top()
	d.Models = models.sorted()
	d.Provider = providers.top()
	for _, s := range subagents.sorted() {
		d.Subagents = append(d.Subagents, s.Value)
	}
	for _, n := range toolOrder {
		d.Tools = append(d.Tools, *tools[n])
	}
	sort.SliceStable(d.Tools, func(i, j int) bool { return d.Tools[i].Calls > d.Tools[j].Calls })
	for n := range declared {
		d.ToolsDeclared = append(d.ToolsDeclared, n)
	}
	sort.Strings(d.ToolsDeclared)
	d.Conversations = len(convTraces)
	for _, ts := range convTraces {
		if len(ts) > 1 {
			d.MultiTurn = true
		}
	}
	if k := routes.top(); k != "" {
		r := routeInfo[k]
		r.Count = routes.n[k]
		d.Route = &r
	}

	// Identity and evidence.
	switch {
	case d.Name != "":
		d.evidence("name", d.Name, "gen_ai.agent.name on invoke_agent spans")
	case d.Service != "":
		d.Name = d.Service
		d.evidence("name", d.Name, "service.name of the resource (no agent span named the agent)")
	}
	d.ID = Slug(d.Name)
	if d.Framework != "" {
		d.evidence("framework", d.Framework, frameworkFrom)
	}
	if len(d.Models) > 0 {
		d.evidence("model", d.Models[0].Value, "gen_ai.request.model or llm.model_name on LLM spans")
	}
	if d.Provider == "" && len(d.Models) > 0 {
		d.Provider = providerFromModel(d.Models[0].Value)
		if d.Provider != "" {
			d.evidence("provider", d.Provider, "the model name")
		}
	} else if d.Provider != "" {
		d.evidence("provider", d.Provider, "gen_ai.provider.name, gen_ai.system or llm.provider")
	}
	if len(d.Tools) > 0 {
		names := make([]string, 0, len(d.Tools))
		for _, t := range d.Tools {
			names = append(names, t.Name)
		}
		d.evidence("tools", strings.Join(names, ", "), "execute_tool or TOOL spans")
	}
	if d.Route != nil {
		d.evidence("route", d.Route.Method+" "+d.Route.Path, "HTTP server span (http.route, url.path, server.address)")
	}

	// Hints.
	if len(d.Tools) > 0 || len(d.ToolsDeclared) > 0 {
		d.Capabilities = append(d.Capabilities, "tools")
		d.Packs = append(d.Packs, "support")
	}
	if d.MultiTurn {
		d.Capabilities = append(d.Capabilities, "sessions")
		d.Packs = append(d.Packs, "memory")
	} else if d.MemoryOps > 0 {
		d.Packs = append(d.Packs, "memory")
	}
	if d.Retrievals > 0 {
		d.Packs = append(d.Packs, "research")
	}

	d.propose()
	return d
}

func (d *Draft) evidence(field, value, from string) {
	d.Evidence = append(d.Evidence, Evidence{Field: field, Value: value, From: from})
}

// propose picks the connect template and its parameters.
func (d *Draft) propose() {
	if len(d.Models) > 0 {
		d.Model = &ModelRef{Provider: d.Provider, Model: d.Models[0].Value}
	}
	r := d.Route
	switch {
	case r != nil && r.URL != "" && !r.Private:
		switch {
		case isA2APath(r.Path):
			d.Template = TemplateA2A
			d.Params = map[string]string{"card_url": strings.TrimSuffix(r.Scheme+"://"+r.Host, "/") + "/.well-known/agent-card.json"}
			d.Preset = "a2a"
		case strings.HasSuffix(r.Path, "/chat/completions"):
			d.Template, d.Preset = TemplateOpenAI, "chat_completions"
			d.Params = map[string]string{"url": r.URL}
		case strings.HasSuffix(r.Path, "/responses"):
			d.Template, d.Preset = TemplateOpenAI, "openai_responses"
			d.Params = map[string]string{"url": r.URL}
		default:
			d.Template = TemplateHTTP
			d.Params = map[string]string{"url": r.URL}
			d.Preset = presetFor(d.Framework, r.Path)
			if strings.Contains(r.Path, "{") {
				d.Params["url"] = r.Scheme + "://" + r.Host
				d.Questions = append(d.Questions, "The route "+r.Path+" has path parameters. Pick the "+d.Preset+" preset on the connect form, or set the request path.")
			}
		}
		d.Questions = append(d.Questions, "Does "+r.Host+" need a token? Add it as a secret and name it here; traces never carry auth headers.")
	case r != nil:
		d.Questions = append(d.Questions, "Your agent serves "+r.Method+" "+r.Path+" on "+hostOr(r.Host, "a host the traces do not name")+
			", which the shared worker cannot reach. Give its public URL, or run heimdall serve next to it (it connects out only).")
		d.Preset = presetFor(d.Framework, r.Path)
	default:
		d.Questions = append(d.Questions, "No HTTP server span in your traces, so the URL your agent is served on is unknown. Give its URL, or wrap the app.")
		if wrapFramework(d.Framework) != "" {
			d.Template = TemplateWrapApp
			d.Params = map[string]string{"framework": wrapFramework(d.Framework)}
			d.Questions = append(d.Questions, "To wrap the app: its git repository or image, and the command that starts its server.")
		}
	}
	if d.Model == nil {
		d.Questions = append(d.Questions, "No model name in your traces. Which model does the agent use?")
	}
	if len(d.Tools) > 0 && len(d.ToolsDeclared) == 0 {
		d.Questions = append(d.Questions, "Only the tools the agent called are known. It may have more.")
	}
	switch {
	case d.Name != "" && d.Model != nil && r != nil && r.URL != "" && !r.Private:
		d.Confidence = ConfidenceHigh
	case d.Name != "" && d.Model != nil:
		d.Confidence = ConfidenceMedium
	default:
		d.Confidence = ConfidenceLow
	}
	if d.ID == "" {
		d.ID = "traced-agent"
	}
}

func hostOr(h, def string) string {
	if h == "" {
		return def
	}
	return h
}

func latest(sp agentevents.Span) time.Time {
	if !sp.End.IsZero() {
		return sp.End
	}
	return sp.Start
}

func first(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(attrs[k]); v != "" {
			return v
		}
	}
	return ""
}

// frameworkRule maps an instrumentation scope (or span name) to a framework.
type frameworkRule struct {
	match     func(scope, name string) bool
	framework string
}

func scopeHas(parts ...string) func(scope, name string) bool {
	return func(scope, _ string) bool {
		for _, p := range parts {
			if strings.Contains(scope, p) {
				return true
			}
		}
		return false
	}
}

var frameworkRules = []frameworkRule{
	{func(scope, name string) bool { return strings.Contains(name, "langgraph") }, "langgraph"},
	{scopeHas("langgraph"), "langgraph"},
	{scopeHas("crewai"), "crewai"},
	{scopeHas("openai_agents", "openai-agents", "openai.agents"), "openai-agents"},
	{scopeHas("pydantic-ai", "pydantic_ai"), "pydantic-ai"},
	{scopeHas("llama_index", "llamaindex", "llama-index"), "llamaindex"},
	{scopeHas("smolagents"), "smolagents"},
	{scopeHas("dspy"), "dspy"},
	{scopeHas("gcp.vertex.agent", "google_adk", "google.adk"), "adk"},
	{scopeHas("mastra"), "mastra"},
	{scopeHas("strands"), "strands-agents"},
	{scopeHas("semantic_kernel", "semantic-kernel", "microsoft.semantickernel"), "semantic-kernel"},
	{scopeHas("agent_framework", "microsoft.agents"), "agent-framework"},
	{scopeHas("autogen"), "autogen"},
	{scopeHas("haystack"), "haystack"},
	{scopeHas("langchain"), "langchain"},
	{func(scope, name string) bool { return scope == "ai" || strings.HasPrefix(name, "ai.generatetext") || strings.HasPrefix(name, "ai.streamtext") }, "ai-sdk"},
	{scopeHas("instrumentation.openai", "opentelemetry.instrumentation.openai", "openai.openai"), "plain-loop"},
	{scopeHas("instrumentation.anthropic"), "plain-loop"},
}

// frameworkOf reads the framework from the instrumentation scope name, or a
// framework attribute some instrumentations set.
func frameworkOf(sp agentevents.Span) (string, string) {
	scope := strings.ToLower(sp.Scope)
	name := strings.ToLower(sp.Name)
	if fw := strings.ToLower(first(sp.Attributes, "gen_ai.framework", "agent.framework")); fw != "" {
		return fw, "gen_ai.framework attribute"
	}
	for _, r := range frameworkRules {
		if r.match(scope, name) {
			why := "instrumentation scope " + sp.Scope
			if sp.Scope == "" {
				why = "span name " + sp.Name
			}
			return r.framework, why
		}
	}
	return "", ""
}

// wrapFramework is the wrap-app template's framework hint.
func wrapFramework(fw string) string {
	switch fw {
	case "langgraph", "langchain":
		return "langgraph"
	case "crewai":
		return "crewai"
	case "openai-agents":
		return "openai-agents"
	case "":
		return ""
	}
	return "custom"
}

// presetFor names the Y10 endpoint preset for a framework and route.
func presetFor(fw, path string) string {
	switch {
	case fw == "langgraph" || strings.Contains(path, "/runs/wait") || strings.Contains(path, "/threads/"):
		return "langgraph"
	case fw == "adk" || strings.HasSuffix(path, "/run_sse"):
		return "adk"
	case fw == "mastra" || strings.Contains(path, "/api/agents/"):
		return "mastra"
	case fw == "ai-sdk":
		return "vercel_ai"
	}
	return "custom"
}

func isA2APath(p string) bool {
	return strings.Contains(p, "/.well-known/agent-card.json") || strings.Contains(p, "/.well-known/agent.json")
}

// providerOf reads the provider from GenAI or OpenInference attributes and
// maps it to a Bifrost provider name.
func providerOf(a map[string]string) string {
	p := strings.ToLower(first(a, "gen_ai.provider.name", "gen_ai.system", "llm.provider", "llm.system"))
	switch {
	case p == "":
		return ""
	case strings.Contains(p, "azure"):
		return "azure"
	case strings.Contains(p, "bedrock") || p == "aws":
		return "bedrock"
	case strings.Contains(p, "vertex"):
		return "vertex"
	case strings.Contains(p, "gemini") || p == "google" || p == "gcp.gen_ai":
		return "gemini"
	case strings.Contains(p, "anthropic"):
		return "anthropic"
	case strings.Contains(p, "openai"):
		return "openai"
	case strings.Contains(p, "mistral"):
		return "mistral"
	case strings.Contains(p, "groq"):
		return "groq"
	case strings.Contains(p, "cohere"):
		return "cohere"
	case strings.Contains(p, "ollama"):
		return "ollama"
	case strings.Contains(p, "deepseek"):
		return "deepseek"
	case strings.Contains(p, "xai") || p == "x_ai":
		return "xai"
	case strings.Contains(p, "openrouter"):
		return "openrouter"
	}
	return ""
}

// providerFromModel guesses the provider from a model name.
func providerFromModel(m string) string {
	m = strings.ToLower(m)
	if i := strings.Index(m, "/"); i > 0 { // provider/model
		if p := providerOf(map[string]string{"gen_ai.system": m[:i]}); p != "" {
			return p
		}
	}
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gpt") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4"):
		return "openai"
	case strings.HasPrefix(m, "gemini"):
		return "gemini"
	case strings.HasPrefix(m, "mistral") || strings.HasPrefix(m, "codestral"):
		return "mistral"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "grok"):
		return "xai"
	}
	return ""
}

// declaredTools reads tool names from opt-in tool definitions:
// gen_ai.tool.definitions (a JSON array) or OpenInference
// llm.tools.<i>.tool.json_schema.
func declaredTools(a map[string]string) []string {
	var out []string
	if v := a["gen_ai.tool.definitions"]; v != "" {
		var defs []map[string]any
		if json.Unmarshal([]byte(v), &defs) == nil {
			for _, d := range defs {
				if n := toolName(d); n != "" {
					out = append(out, n)
				}
			}
		}
	}
	for k, v := range a {
		if strings.HasPrefix(k, "llm.tools.") && strings.HasSuffix(k, ".tool.json_schema") {
			var d map[string]any
			if json.Unmarshal([]byte(v), &d) == nil {
				if n := toolName(d); n != "" {
					out = append(out, n)
				}
			}
		}
		if strings.HasPrefix(k, "heimdall.tool_declared.") {
			out = append(out, strings.TrimPrefix(k, "heimdall.tool_declared."))
		}
	}
	return out
}

func toolName(d map[string]any) string {
	if n, ok := d["name"].(string); ok && n != "" {
		return n
	}
	if f, ok := d["function"].(map[string]any); ok {
		if n, ok := f["name"].(string); ok {
			return n
		}
	}
	return ""
}

// routeOf reads the serving route of an HTTP server span. Client spans (the
// agent calling its model or a tool) are skipped.
func routeOf(sp agentevents.Span) (Route, bool) {
	a := sp.Attributes
	method := strings.ToUpper(first(a, "http.request.method", "http.method"))
	if method == "" || sp.Kind == "client" {
		return Route{}, false
	}
	if sp.Kind != "server" && a["http.route"] == "" {
		return Route{}, false
	}
	path := first(a, "http.route", "url.path", "http.target")
	full := first(a, "url.full", "http.url")
	var u *url.URL
	if full != "" {
		if pu, err := url.Parse(full); err == nil && pu.Host != "" {
			u = pu
		}
	}
	if path == "" && u != nil {
		path = u.Path
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return Route{}, false
	}
	if method == "GET" && !isA2APath(path) || method == "OPTIONS" || method == "HEAD" {
		return Route{}, false // health checks and pages, not the agent call
	}
	if strings.HasSuffix(path, "/health") || strings.HasSuffix(path, "/healthz") || strings.HasSuffix(path, "/metrics") {
		return Route{}, false
	}
	scheme := first(a, "url.scheme", "http.scheme")
	host := first(a, "server.address", "http.host", "net.host.name")
	port := first(a, "server.port", "net.host.port")
	if u != nil {
		if scheme == "" {
			scheme = u.Scheme
		}
		if host == "" {
			host = u.Host
		}
	}
	if host != "" && port != "" && !strings.Contains(host, ":") && port != "80" && port != "443" {
		host = net.JoinHostPort(host, port)
	}
	if scheme == "" {
		scheme = "https"
		if p := portOf(host); p != "" && p != "443" {
			scheme = "http"
		}
	}
	r := Route{Method: method, Scheme: scheme, Host: host, Path: templatePath(path)}
	if host != "" {
		r.URL = scheme + "://" + host + r.Path
		r.Private = privateHost(host)
	}
	return r, true
}

func portOf(host string) string {
	if _, p, err := net.SplitHostPort(host); err == nil {
		return p
	}
	return ""
}

var (
	uuidSeg = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	numSeg  = regexp.MustCompile(`^[0-9]+$`)
)

// templatePath replaces id-like path segments (uuids, numbers) with {id},
// so url.path values of one route count together.
func templatePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if uuidSeg.MatchString(s) || numSeg.MatchString(s) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

// privateHost reports whether the shared worker would refuse to call host:
// loopback, private and link-local addresses, and names with no dot (a
// cluster or compose service name).
func privateHost(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	l := strings.ToLower(h)
	return l == "localhost" || !strings.Contains(l, ".") || strings.HasSuffix(l, ".local") || strings.HasSuffix(l, ".internal") ||
		strings.HasSuffix(l, ".svc") || strings.Contains(l, ".svc.cluster.local")
}

var slugBad = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a name into an agent id: lowercase letters, digits and
// hyphens, at most 48 characters.
func Slug(name string) string {
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s != "" && (s[0] < 'a' || s[0] > 'z') {
		s = "agent-" + s
	}
	return s
}

// Summary is one line naming what the traces show, such as
// "support-bot, langgraph, gpt-5.4-mini, tools seen: lookup_order, refund,
// served at POST /chat".
func (d Draft) Summary() string {
	parts := []string{}
	if d.Name != "" {
		parts = append(parts, d.Name)
	}
	if d.Framework != "" {
		parts = append(parts, d.Framework)
	}
	if len(d.Models) > 0 {
		parts = append(parts, d.Models[0].Value)
	}
	if len(d.Tools) > 0 {
		names := make([]string, 0, len(d.Tools))
		for i, t := range d.Tools {
			if i == 6 {
				names = append(names, "+"+strconv.Itoa(len(d.Tools)-6)+" more")
				break
			}
			names = append(names, t.Name)
		}
		parts = append(parts, "tools seen: "+strings.Join(names, ", "))
	}
	if d.Route != nil {
		parts = append(parts, "served at "+d.Route.Method+" "+d.Route.Path)
	}
	if len(parts) == 0 {
		return "nothing about an agent yet"
	}
	return strings.Join(parts, ", ")
}
