package agentevents

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// span is the part of an OTLP span this package reads, from either
// encoding.
type span struct {
	traceID, spanID, parentID, name string
	start, end                      time.Time
	attrs                           map[string]string
	statusCode                      int // 0 unset, 1 ok, 2 error
	statusMessage                   string
	service                         string
	// Read for agent inference (spans.go): the span kind (2 server, 3
	// client), the instrumentation scope and the resource attributes.
	kind                int
	scope, scopeVersion string
	resource            map[string]string
}

// OTLP content types.
const (
	ContentTypeOTLPProto = "application/x-protobuf"
	ContentTypeOTLPJSON  = "application/json"
)

// ParseOTLP decodes an OTLP/HTTP trace export request
// (ExportTraceServiceRequest) in the protobuf or JSON encoding, chosen by the
// content type (protobuf when it says protobuf, JSON otherwise), and maps
// each span onto a record. It returns the records, how many spans were
// refused, and an error when the body is not a trace export at all.
func ParseOTLP(data []byte, contentType string, lim Limits, now time.Time) ([]Record, int, error) {
	lim = lim.WithDefaults()
	mt, _, _ := mime.ParseMediaType(contentType)
	var spans []span
	var err error
	if strings.Contains(mt, "protobuf") {
		spans, err = decodeProtoRequest(data)
	} else {
		spans, err = decodeJSONRequest(data)
	}
	if err != nil {
		return nil, 0, err
	}
	out := make([]Record, 0, len(spans))
	bad := 0
	agentSpans := map[string]bool{}
	for _, sp := range spans {
		if spanType(sp) == TypeAgent {
			agentSpans[sp.traceID+"/"+sp.spanID] = true
		}
	}
	for _, sp := range spans {
		if sp.spanID == "" {
			bad++
			continue
		}
		out = append(out, spanRecord(sp, agentSpans, lim, now))
	}
	return out, bad, nil
}

// spanType classifies a span by the GenAI conventions
// (gen_ai.operation.name) and the OpenInference ones
// (openinference.span.kind).
func spanType(sp span) string {
	op := strings.ToLower(sp.attrs["gen_ai.operation.name"])
	kind := strings.ToUpper(sp.attrs["openinference.span.kind"])
	name := strings.ToLower(sp.name)
	switch {
	case op == "execute_tool" || op == "tool_call" || kind == "TOOL" || (sp.attrs["gen_ai.tool.name"] != "" && op == "") || name == "claude_code.tool":
		return TypeToolCall
	case op == "invoke_agent" || op == "create_agent" || kind == "AGENT":
		return TypeAgent
	case op == "handoff" || strings.HasPrefix(name, "handoff"):
		return TypeHandoff
	case op == "chat" || op == "text_completion" || op == "generate_content" || op == "embeddings" || op == "llm_call" || kind == "LLM" || kind == "EMBEDDING" || name == "claude_code.llm_request":
		return TypeLLM
	case op == "retrieval" || kind == "RETRIEVER" || kind == "RERANKER" || strings.HasPrefix(name, "retriev"):
		return TypeRetrieval
	case strings.Contains(name, "memory") && (strings.Contains(name, "write") || strings.Contains(name, "save") || strings.Contains(name, "store")):
		return TypeMemoryWrite
	case strings.Contains(name, "memory"):
		return TypeMemoryRead
	case strings.HasPrefix(name, "plan"):
		return TypePlan
	}
	return TypeSpan
}

// spanRecord maps one span onto a record. An agent span inside another
// agent span is a subagent run.
func spanRecord(sp span, agentSpans map[string]bool, lim Limits, now time.Time) Record {
	r := Record{
		Channel:  schemas.ReportChannelOTLP,
		Type:     spanType(sp),
		ID:       sp.spanID,
		ParentID: sp.parentID,
		TraceID:  sp.traceID,
		Name:     sp.name,
		Start:    sp.start,
		End:      sp.end,
		Received: now.UTC(),
		Model:    first(sp.attrs, "gen_ai.response.model", "gen_ai.request.model", "llm.model_name"),
	}
	switch sp.statusCode {
	case 2:
		r.Status = StatusError
	case 1:
		r.Status = StatusOK
	default:
		if !sp.end.IsZero() {
			r.Status = StatusOK
		}
	}
	switch r.Type {
	case TypeToolCall:
		r.Name = first(sp.attrs, "gen_ai.tool.name", "tool.name", "tool_name")
		if r.Name == "" {
			r.Name = sp.name
		}
		r.ToolCallID = first(sp.attrs, "gen_ai.tool.call.id", "tool_call.id", "tool.id")
		r.Input = first(sp.attrs, "gen_ai.tool.call.arguments", "tool.parameters", "input.value")
		r.Output = first(sp.attrs, "gen_ai.tool.call.result", "output.value")
	case TypeAgent:
		if n := first(sp.attrs, "gen_ai.agent.name", "agent.name"); n != "" {
			r.Name = n
		}
		if sp.parentID != "" && hasAgentAncestor(sp, agentSpans) {
			r.Type = TypeSubagentStart
		}
	default:
		r.Input = first(sp.attrs, "input.value")
		r.Output = first(sp.attrs, "output.value")
	}
	if r.Status == StatusError && r.Output == "" {
		r.Output = sp.statusMessage
		if r.Output == "" {
			r.Output = sp.attrs["error.type"]
		}
	}
	if r.Start.IsZero() {
		r.Start = r.Received
	}
	if !r.End.IsZero() && r.End.Before(r.Start) {
		r.End = time.Time{}
	}
	attrs := map[string]string{}
	if sp.service != "" {
		attrs["service.name"] = sp.service
	}
	for _, k := range []string{"gen_ai.operation.name", "openinference.span.kind", "gen_ai.agent.name", "gen_ai.system", "gen_ai.provider.name"} {
		if v := sp.attrs[k]; v != "" {
			attrs[k] = v
		}
	}
	for _, k := range agentKeys { // Multi-agent area: agent identity attributes (agents.go)
		if v := sp.attrs[k]; v != "" {
			attrs[k] = v
		}
	}
	if len(attrs) > 0 {
		r.Attributes = attrs
	}
	sanitize(&r, lim)
	return r
}

// hasAgentAncestor reports whether the span's parent is an agent span. Only
// the direct parent is known from one span; a deeper agent ancestor is found
// when events are normalized (subagent nesting goes by parent links).
func hasAgentAncestor(sp span, agentSpans map[string]bool) bool {
	return agentSpans[sp.traceID+"/"+sp.parentID]
}

func first(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

// decodeJSONRequest reads the OTLP/JSON encoding: lowerCamelCase field names
// (snake_case accepted too), hex trace and span ids, 64-bit integers as
// strings or numbers.
func decodeJSONRequest(data []byte) ([]span, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, unsupported("OTLP JSON: %v", err)
	}
	rsv := field(root, "resourceSpans", "resource_spans")
	if rsv == nil {
		return nil, unsupported("OTLP JSON: no resourceSpans")
	}
	rs, _ := rsv.([]any)
	var out []span
	for _, r := range rs {
		rm, _ := r.(map[string]any)
		service := ""
		var resource map[string]string
		if res, ok := field(rm, "resource").(map[string]any); ok {
			resource = jsonAttrs(field(res, "attributes"))
			service = resource["service.name"]
		}
		scopes, _ := field(rm, "scopeSpans", "scope_spans", "instrumentationLibrarySpans").([]any)
		for _, ss := range scopes {
			ssm, _ := ss.(map[string]any)
			spans, _ := ssm["spans"].([]any)
			scope, scopeVersion := "", ""
			if sc, ok := field(ssm, "scope", "instrumentationLibrary").(map[string]any); ok {
				scope, scopeVersion = asString(sc["name"]), asString(sc["version"])
			}
			for _, raw := range spans {
				m, _ := raw.(map[string]any)
				if m == nil {
					continue
				}
				sp := span{
					traceID:  jsonID(field(m, "traceId", "trace_id")),
					spanID:   jsonID(field(m, "spanId", "span_id")),
					parentID: jsonID(field(m, "parentSpanId", "parent_span_id")),
					name:     asString(m["name"]),
					start:    jsonNanos(field(m, "startTimeUnixNano", "start_time_unix_nano")),
					end:      jsonNanos(field(m, "endTimeUnixNano", "end_time_unix_nano")),
					attrs:    jsonAttrs(m["attributes"]),
					service:  service,
					kind:     jsonSpanKind(m["kind"]),
					resource: resource,
					scope:    scope,
				}
				sp.scopeVersion = scopeVersion
				if st, ok := m["status"].(map[string]any); ok {
					sp.statusCode = statusCode(st["code"])
					sp.statusMessage = asString(st["message"])
				}
				out = append(out, sp)
			}
		}
	}
	return out, nil
}

func field(m map[string]any, names ...string) any {
	for _, n := range names {
		if v, ok := m[n]; ok {
			return v
		}
	}
	return nil
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// jsonID reads a trace or span id: hex as the OTLP/JSON encoding mandates,
// or base64 as some exporters wrongly send.
func jsonID(v any) string {
	s := strings.TrimSpace(asString(v))
	if s == "" {
		return ""
	}
	if _, err := hex.DecodeString(s); err == nil {
		return strings.ToLower(s)
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && (len(b) == 8 || len(b) == 16) {
		return hex.EncodeToString(b)
	}
	return s
}

func jsonNanos(v any) time.Time {
	s := asString(v)
	if s == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	t := time.Unix(0, n).UTC()
	if t.Year() < 2000 || t.Year() > 2200 {
		return time.Time{}
	}
	return t
}

// jsonSpanKind reads a span kind: the enum number or its name.
func jsonSpanKind(v any) int {
	switch s := strings.ToUpper(asString(v)); s {
	case "1", "SPAN_KIND_INTERNAL":
		return 1
	case "2", "SPAN_KIND_SERVER":
		return 2
	case "3", "SPAN_KIND_CLIENT":
		return 3
	case "4", "SPAN_KIND_PRODUCER":
		return 4
	case "5", "SPAN_KIND_CONSUMER":
		return 5
	}
	return 0
}

func statusCode(v any) int {
	switch s := asString(v); s {
	case "2", "STATUS_CODE_ERROR":
		return 2
	case "1", "STATUS_CODE_OK":
		return 1
	}
	return 0
}

// jsonAttrs flattens OTLP/JSON key-value attributes into text.
func jsonAttrs(v any) map[string]string {
	out := map[string]string{}
	list, _ := v.([]any)
	for _, kv := range list {
		m, _ := kv.(map[string]any)
		k := asString(m["key"])
		if k == "" || len(out) >= 256 {
			continue
		}
		out[k] = jsonAnyValue(m["value"], 0)
	}
	return out
}

func jsonAnyValue(v any, depth int) string {
	m, _ := v.(map[string]any)
	if m == nil || depth > 4 {
		return ""
	}
	for _, k := range []string{"stringValue", "string_value", "intValue", "int_value", "doubleValue", "double_value", "boolValue", "bool_value", "bytesValue", "bytes_value"} {
		if x, ok := m[k]; ok {
			return asString(x)
		}
	}
	if a, ok := field(m, "arrayValue", "array_value").(map[string]any); ok {
		vals, _ := a["values"].([]any)
		parts := make([]string, 0, len(vals))
		for _, x := range vals {
			parts = append(parts, strconv.Quote(jsonAnyValue(x, depth+1)))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	if kv, ok := field(m, "kvlistValue", "kvlist_value").(map[string]any); ok {
		inner := jsonAttrs(kv["values"])
		keys := make([]string, 0, len(inner))
		for k := range inner {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q:%q", k, inner[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return ""
}
