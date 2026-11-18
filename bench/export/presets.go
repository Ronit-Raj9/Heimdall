package export

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/traceexport"
)

// OTLP body encodings.
const (
	EncodingJSON     = "json"     // application/json (OTLP/JSON)
	EncodingProtobuf = "protobuf" // application/x-protobuf
)

// PresetHeader is one header a backend requires. The value is built from
// environment variables, never from a flag, so a key never lands in shell
// history or a process list.
type PresetHeader struct {
	Name     string   // header name as the backend documents it
	Env      []string // environment variables the value is built from
	Fixed    string   // a constant value (no Env)
	Build    func(vals []string) string
	Optional bool // sent only when every Env variable is set
}

// Preset is what one observability backend documents for OTLP/HTTP trace
// ingestion: the traces URL, the headers, the body encoding and the span
// attributes it reads to render a span as an LLM call, a tool call or an
// agent. Source and Checked say where and when that was read; Unverified
// lists what the documents did not settle.
type Preset struct {
	Name       string
	Endpoint   string // default traces URL; empty means -endpoint is required
	Encoding   string
	Headers    []PresetHeader
	Resource   map[string]string // resource attributes added to every run
	Map        func(td *traceexport.TracesData)
	Source     []string
	Checked    string
	Unverified []string
}

// presetChecked is the date every preset was read from its documents.
const presetChecked = "2026-11-18"

// Presets lists every backend preset by name.
func Presets() map[string]Preset {
	return map[string]Preset{
		"langfuse": {
			Name: "langfuse", Endpoint: "https://cloud.langfuse.com/api/public/otel/v1/traces", Encoding: EncodingJSON,
			Headers: []PresetHeader{
				{Name: "Authorization", Env: []string{"LANGFUSE_PUBLIC_KEY", "LANGFUSE_SECRET_KEY"}, Build: func(v []string) string {
					return "Basic " + base64.StdEncoding.EncodeToString([]byte(v[0]+":"+v[1]))
				}},
				{Name: "x-langfuse-ingestion-version", Fixed: "4"},
			},
			Map:        mapLangfuse,
			Source:     []string{"https://langfuse.com/integrations/native/opentelemetry"},
			Checked:    presetChecked,
			Unverified: []string{"the variable names LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY follow the Langfuse SDKs; the OpenTelemetry page names only the Basic auth string", "the default endpoint is the EU cloud region; pass -endpoint for US, JP, HIPAA or self-hosted"},
		},
		"langsmith": {
			Name: "langsmith", Endpoint: "https://api.smith.langchain.com/otel/v1/traces", Encoding: EncodingProtobuf,
			Headers: []PresetHeader{
				{Name: "x-api-key", Env: []string{"LANGSMITH_API_KEY"}, Build: first},
				{Name: "Langsmith-Project", Env: []string{"LANGSMITH_PROJECT"}, Build: first, Optional: true},
			},
			Map:        mapLangSmith,
			Source:     []string{"https://docs.langchain.com/langsmith/trace-with-opentelemetry"},
			Checked:    presetChecked,
			Unverified: []string{"the page does not say whether OTLP/JSON is accepted, so the preset sends protobuf (the OpenTelemetry exporters' default)", "LANGSMITH_PROJECT as the variable for the Langsmith-Project header is the preset's choice"},
		},
		"braintrust": {
			Name: "braintrust", Endpoint: "https://api.braintrust.dev/otel/v1/traces", Encoding: EncodingProtobuf,
			Headers: []PresetHeader{
				{Name: "Authorization", Env: []string{"BRAINTRUST_API_KEY"}, Build: func(v []string) string { return "Bearer " + v[0] }},
				{Name: "x-bt-parent", Env: []string{"BRAINTRUST_PROJECT_ID"}, Build: func(v []string) string { return "project_id:" + v[0] }},
			},
			Map: mapBraintrust,
			Source: []string{"https://docs.braintrust.dev/integrations/sdk-integrations/opentelemetry/send-traces-and-logs",
				"https://docs.braintrust.dev/integrations/sdk-integrations/opentelemetry/attributes"},
			Checked:    presetChecked,
			Unverified: []string{"the page states JSON and protobuf for the logs endpoint only; for traces the preset sends protobuf"},
		},
		"datadog": {
			Name: "datadog", Encoding: EncodingProtobuf,
			Headers: []PresetHeader{
				{Name: "dd-api-key", Env: []string{"DD_API_KEY"}, Build: first},
				{Name: "dd-otlp-source", Fixed: "llmobs"},
				{Name: "DD-LLMObs-Enabled", Fixed: "true"},
			},
			Resource: map[string]string{"dd_llmobs_enabled": "true"},
			Map:      mapDatadog,
			Source: []string{"https://docs.datadoghq.com/llm_observability/instrumentation/otel_instrumentation/",
				"https://docs.datadoghq.com/opentelemetry/setup/otlp_ingest/traces/"},
			Checked:    presetChecked,
			Unverified: []string{"the trace intake host depends on the Datadog site and the page fills it in by script, so there is no default: pass -endpoint (it must end in /v1/traces)"},
		},
		"maxim": {
			Name: "maxim", Endpoint: "https://api.getmaxim.ai/v1/otel", Encoding: EncodingJSON,
			Headers: []PresetHeader{
				{Name: "x-maxim-api-key", Env: []string{"MAXIM_API_KEY"}, Build: first},
				{Name: "x-maxim-repo-id", Env: []string{"MAXIM_LOG_REPO_ID"}, Build: first},
			},
			Map:        mapMaxim,
			Source:     []string{"https://www.getmaxim.ai/docs/tracing/opentelemetry/ingesting-via-otlp"},
			Checked:    presetChecked,
			Unverified: []string{"MAXIM_LOG_REPO_ID as the variable for the repository id is the preset's choice"},
		},
		"phoenix": {
			Name: "phoenix", Endpoint: "http://localhost:6006/v1/traces", Encoding: EncodingProtobuf,
			Headers: []PresetHeader{
				{Name: "Authorization", Env: []string{"PHOENIX_API_KEY"}, Build: func(v []string) string { return "Bearer " + v[0] }, Optional: true},
			},
			Resource: map[string]string{"openinference.project.name": "heimdall"},
			Map:      mapPhoenix,
			Source: []string{"https://arize.com/docs/phoenix/tracing/concepts-tracing/otel-openinference/exporter",
				"https://arize.com/docs/phoenix/tracing/concepts-tracing/otel-openinference/span-kinds",
				"https://arize.com/docs/phoenix/tracing/concepts-tracing/otel-openinference/resource"},
			Checked:    presetChecked,
			Unverified: []string{"whether Phoenix's HTTP receiver accepts OTLP/JSON is not stated; the preset sends protobuf"},
		},
		"jaeger": {
			Name: "jaeger", Endpoint: "http://localhost:4318/v1/traces", Encoding: EncodingJSON,
			Source:  []string{"https://www.jaegertracing.io/docs/latest/architecture/apis/"},
			Checked: presetChecked,
		},
	}
}

// PresetNames returns the preset names, sorted.
func PresetNames() []string {
	ps := Presets()
	out := make([]string, 0, len(ps))
	for n := range ps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func first(v []string) string { return v[0] }

// ErrMissingCredential is returned when a required header variable is unset.
var ErrMissingCredential = errors.New("credential variable not set")

// PresetHeaders builds the preset's headers from the environment (lookup is
// os.LookupEnv when nil). A required variable that is unset or empty is an
// error naming the variable, never its value.
func (p Preset) PresetHeaders(lookup func(string) (string, bool)) (map[string]string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	out := map[string]string{}
	for _, h := range p.Headers {
		if len(h.Env) == 0 {
			out[h.Name] = h.Fixed
			continue
		}
		vals := make([]string, len(h.Env))
		missing := ""
		for i, name := range h.Env {
			v, ok := lookup(name)
			if !ok || strings.TrimSpace(v) == "" {
				missing = name
				break
			}
			vals[i] = strings.TrimSpace(v)
		}
		if missing != "" {
			if h.Optional {
				continue
			}
			return nil, fmt.Errorf("%s preset: header %s: %w: %s", p.Name, h.Name, ErrMissingCredential, missing)
		}
		v := h.Build(vals)
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("%s preset: header %s: line breaks are not allowed", p.Name, h.Name)
		}
		out[h.Name] = v
	}
	return out, nil
}

// Apply adds the preset's resource attributes and attribute mapping to a
// copy-free trace in place. Attributes already set are never overwritten.
func (p Preset) Apply(td *traceexport.TracesData) {
	if td == nil {
		return
	}
	if len(p.Resource) > 0 {
		keys := make([]string, 0, len(p.Resource))
		for k := range p.Resource {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i := range td.ResourceSpans {
			for _, k := range keys {
				setAttr(&td.ResourceSpans[i].Resource.Attributes, traceexport.Str(k, p.Resource[k]))
			}
		}
	}
	if p.Map != nil {
		p.Map(td)
	}
}

// Encode marshals the trace in the preset's encoding and returns the body
// and its content type.
func (p Preset) Encode(td *traceexport.TracesData) ([]byte, string, error) {
	if p.Encoding == EncodingProtobuf {
		b, err := MarshalOTLPProto(td)
		return b, "application/x-protobuf", err
	}
	b, err := traceexport.Marshal(td)
	return b, "application/json", err
}

// setAttr appends kv unless the key is already present.
func setAttr(attrs *[]traceexport.KeyValue, kv traceexport.KeyValue) {
	if _, ok := traceexport.Attr(*attrs, kv.Key); ok {
		return
	}
	*attrs = append(*attrs, kv)
}

// attrStr returns a string attribute.
func attrStr(attrs []traceexport.KeyValue, key string) string {
	v, ok := traceexport.Attr(attrs, key)
	if !ok {
		return ""
	}
	return v.String()
}

// attrInt returns an integer attribute.
func attrInt(attrs []traceexport.KeyValue, key string) (int64, bool) {
	v, ok := traceexport.Attr(attrs, key)
	if !ok || v.IntValue == nil {
		return 0, false
	}
	return int64(*v.IntValue), true
}

// spanRole classifies a span of a Heimdall trace: run (the root), llm (a
// model call), tool, agent (a session), grade, gate or other.
func spanRole(sp *traceexport.Span) string {
	switch {
	case sp.ParentSpanID == "":
		return "run"
	case strings.HasPrefix(sp.Name, "heimdall.grade"):
		return "grade"
	case strings.HasPrefix(sp.Name, "heimdall.gate"):
		return "gate"
	}
	switch attrStr(sp.Attributes, "gen_ai.operation.name") {
	case "chat":
		return "llm"
	case "execute_tool":
		return "tool"
	case "invoke_agent":
		return "agent"
	}
	return "other"
}

// eachSpan calls fn for every span.
func eachSpan(td *traceexport.TracesData, fn func(sp *traceexport.Span)) {
	for i := range td.ResourceSpans {
		for j := range td.ResourceSpans[i].ScopeSpans {
			ss := &td.ResourceSpans[i].ScopeSpans[j]
			for k := range ss.Spans {
				fn(&ss.Spans[k])
			}
		}
	}
}

// rootFacts are the run facts the root span carries, for metadata maps.
func rootFacts(sp *traceexport.Span) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"heimdall.experiment", "heimdall.run.id", "heimdall.suite", "heimdall.task", "heimdall.arm",
		"heimdall.condition", "heimdall.outcome", "heimdall.agent.config_id", "heimdall.seal_hash"} {
		if v := attrStr(sp.Attributes, k); v != "" {
			out[strings.ReplaceAll(strings.TrimPrefix(k, "heimdall."), ".", "_")] = v
		}
	}
	if v, ok := traceexport.Attr(sp.Attributes, "heimdall.verified_success"); ok {
		out["verified_success"] = v.String()
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mapLangfuse sets langfuse.observation.type so model calls are generations,
// tool calls tools and sessions agents, the trace name and session id on the
// root, and the run facts as filterable trace metadata.
func mapLangfuse(td *traceexport.TracesData) {
	eachSpan(td, func(sp *traceexport.Span) {
		typ := map[string]string{"llm": "generation", "tool": "tool", "agent": "agent", "grade": "evaluator", "gate": "guardrail", "run": "span"}[spanRole(sp)]
		if typ != "" {
			setAttr(&sp.Attributes, traceexport.Str("langfuse.observation.type", typ))
		}
		if spanRole(sp) == "run" {
			setAttr(&sp.Attributes, traceexport.Str("langfuse.trace.name", sp.Name))
			if s := attrStr(sp.Attributes, "gen_ai.conversation.id"); s != "" {
				setAttr(&sp.Attributes, traceexport.Str("langfuse.session.id", s))
			}
			facts := rootFacts(sp)
			for _, k := range sortedKeys(facts) {
				setAttr(&sp.Attributes, traceexport.Str("langfuse.trace.metadata."+k, facts[k]))
			}
		}
	})
}

// mapLangSmith sets langsmith.span.kind (llm, tool, chain) and the run facts
// as langsmith.metadata on the root.
func mapLangSmith(td *traceexport.TracesData) {
	eachSpan(td, func(sp *traceexport.Span) {
		kind := map[string]string{"llm": "llm", "tool": "tool", "agent": "chain", "run": "chain", "grade": "chain", "gate": "chain"}[spanRole(sp)]
		if kind != "" {
			setAttr(&sp.Attributes, traceexport.Str("langsmith.span.kind", kind))
		}
		if spanRole(sp) == "run" {
			if s := attrStr(sp.Attributes, "gen_ai.conversation.id"); s != "" {
				setAttr(&sp.Attributes, traceexport.Str("langsmith.trace.session_id", s))
			}
			facts := rootFacts(sp)
			for _, k := range sortedKeys(facts) {
				setAttr(&sp.Attributes, traceexport.Str("langsmith.metadata."+k, facts[k]))
			}
		}
	})
}

// mapBraintrust relies on Braintrust's documented gen_ai mapping (chat to
// llm, execute_tool to tool) and marks the root a task with the run facts
// as braintrust.metadata.
func mapBraintrust(td *traceexport.TracesData) {
	eachSpan(td, func(sp *traceexport.Span) {
		switch spanRole(sp) {
		case "run":
			setAttr(&sp.Attributes, traceexport.Str("braintrust.span_attributes.type", "task"))
			facts := rootFacts(sp)
			for _, k := range sortedKeys(facts) {
				setAttr(&sp.Attributes, traceexport.Str("braintrust.metadata."+k, facts[k]))
			}
		case "grade":
			setAttr(&sp.Attributes, traceexport.Str("braintrust.span_attributes.type", "score"))
		}
	})
}

// mapDatadog needs no span attributes beyond the OpenTelemetry GenAI
// conventions the export already writes (gen_ai.operation.name,
// gen_ai.provider.name, gen_ai.request.model, gen_ai.usage.*); the preset's
// resource attribute dd_llmobs_enabled turns on LLM Observability
// conversion. service.name (heimdall) becomes the ml_app.
func mapDatadog(*traceexport.TracesData) {}

// mapMaxim writes maxim.metadata on the root: the trace name, the run facts
// as trace tags and verified success as a trace metric.
func mapMaxim(td *traceexport.TracesData) {
	eachSpan(td, func(sp *traceexport.Span) {
		if spanRole(sp) != "run" {
			return
		}
		facts := rootFacts(sp)
		meta := map[string]any{"maxim-trace-name": sp.Name, "maxim-trace-tags": facts}
		if v, ok := traceexport.Attr(sp.Attributes, "heimdall.verified_success"); ok && v.BoolValue != nil {
			x := 0.0
			if *v.BoolValue {
				x = 1
			}
			meta["maxim-trace-metrics"] = map[string]float64{"verified_success": x}
		}
		b, err := json.Marshal(meta)
		if err == nil {
			setAttr(&sp.Attributes, traceexport.Str("maxim.metadata", string(b)))
		}
	})
}

// mapPhoenix writes the OpenInference attributes Phoenix renders:
// openinference.span.kind (all caps), llm.model_name, llm.provider,
// llm.token_count.*, tool.name and session.id.
func mapPhoenix(td *traceexport.TracesData) {
	eachSpan(td, func(sp *traceexport.Span) {
		role := spanRole(sp)
		kind := map[string]string{"llm": "LLM", "tool": "TOOL", "agent": "AGENT", "run": "CHAIN", "grade": "EVALUATOR", "gate": "GUARDRAIL"}[role]
		if kind != "" {
			setAttr(&sp.Attributes, traceexport.Str("openinference.span.kind", kind))
		}
		if s := attrStr(sp.Attributes, "gen_ai.conversation.id"); s != "" {
			setAttr(&sp.Attributes, traceexport.Str("session.id", s))
		}
		switch role {
		case "llm":
			if m := firstNonEmpty(attrStr(sp.Attributes, "gen_ai.response.model"), attrStr(sp.Attributes, "gen_ai.request.model")); m != "" {
				setAttr(&sp.Attributes, traceexport.Str("llm.model_name", m))
			}
			if p := attrStr(sp.Attributes, "gen_ai.provider.name"); p != "" {
				setAttr(&sp.Attributes, traceexport.Str("llm.provider", p))
			}
			in, okIn := attrInt(sp.Attributes, "gen_ai.usage.input_tokens")
			out, okOut := attrInt(sp.Attributes, "gen_ai.usage.output_tokens")
			if okIn {
				setAttr(&sp.Attributes, traceexport.Int("llm.token_count.prompt", in))
			}
			if okOut {
				setAttr(&sp.Attributes, traceexport.Int("llm.token_count.completion", out))
			}
			if okIn && okOut {
				setAttr(&sp.Attributes, traceexport.Int("llm.token_count.total", in+out))
			}
		case "tool":
			if n := attrStr(sp.Attributes, "gen_ai.tool.name"); n != "" {
				setAttr(&sp.Attributes, traceexport.Str("tool.name", n))
			}
		}
	})
}
