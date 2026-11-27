package traceimport

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file reads OTLP/JSON traces (an ExportTraceServiceRequest as the
// OTLP/HTTP JSON encoding and the collector's file exporter write it) with
// the OpenTelemetry GenAI conventions. It decodes the generic JSON tree with
// its own minimal accessors; it does not share types with the trace exporter.
//
// What a span contributes:
//   - gen_ai.operation.name execute_tool (or any span with gen_ai.tool.name):
//     one tool call from gen_ai.tool.name, gen_ai.tool.call.id,
//     gen_ai.tool.call.arguments, gen_ai.tool.call.result and the span status.
//   - gen_ai.input.messages and gen_ai.output.messages (JSON, role plus parts
//     of type text, tool_call and tool_call_response): the conversation.
//   - the older indexed attributes gen_ai.prompt.<i>.* and
//     gen_ai.completion.<i>.* (including tool_calls.<j>.*), and span events
//     named gen_ai.<role>.message or gen_ai.choice: the conversation too.
//   - gen_ai.response.model or gen_ai.request.model: the model.
//   - a status of ERROR, with its message or error.type: an outcome hint.

// otlpSpan is the part of an OTLP span the importer reads.
type otlpSpan struct {
	traceID, spanID, parentID, name string
	start, end                      time.Time
	attrs                           map[string]any
	events                          []otlpEvent
	statusCode                      int // 0 unset, 1 ok, 2 error
	statusMessage                   string
}

type otlpEvent struct {
	name  string
	time  time.Time
	attrs map[string]any
}

// parseOTLP groups every span of every value by trace id, in the order the
// traces first appear, and builds one session per trace.
func parseOTLP(values []any) ([]*Session, error) {
	byTrace := map[string][]otlpSpan{}
	agentOf := map[string]string{} // trace id -> the resource's service.name (fix wave M7-12)
	var order []string
	for _, v := range values {
		obj := asMap(v)
		if obj == nil {
			return nil, fmt.Errorf("a top-level %s; want an object with resourceSpans", kindOf(v))
		}
		rs := field(obj, "resourceSpans", "resource_spans")
		if rs == nil {
			return nil, fmt.Errorf("an object without resourceSpans (keys %s)", keys(obj))
		}
		for _, r := range asArray(rs) {
			service := asString(attributes(asMap(asMap(r)["resource"])["attributes"])["service.name"])
			for _, ss := range asArray(field(asMap(r), "scopeSpans", "scope_spans", "instrumentationLibrarySpans")) {
				for _, raw := range asArray(asMap(ss)["spans"]) {
					sp := decodeSpan(asMap(raw))
					if sp.traceID == "" {
						continue
					}
					if _, ok := byTrace[sp.traceID]; !ok {
						order = append(order, sp.traceID)
					}
					byTrace[sp.traceID] = append(byTrace[sp.traceID], sp)
					if service != "" && agentOf[sp.traceID] == "" {
						agentOf[sp.traceID] = service
					}
				}
			}
		}
	}
	out := make([]*Session, 0, len(order))
	for _, id := range order {
		sess := otlpSession(id, byTrace[id])
		if sess.Agent == "" {
			sess.Agent = agentName(agentOf[id])
		}
		out = append(out, sess)
	}
	return out, nil
}

// otlpSession builds one session from the spans of one trace.
func otlpSession(id string, spans []otlpSpan) *Session {
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	b := newBuilder(id)
	rootOK := false
	for _, sp := range spans {
		if sp.parentID == "" {
			b.s.Name = sp.name
			rootOK = sp.statusCode == 1
		}
		if b.s.Started.IsZero() || (!sp.start.IsZero() && sp.start.Before(b.s.Started)) {
			b.s.Started = sp.start
		}
		if sp.end.After(b.s.Ended) {
			b.s.Ended = sp.end
		}
		if a := asString(sp.attrs["gen_ai.agent.name"]); a != "" && b.s.Agent == "" {
			b.s.Agent = agentName(a)
		}
		if m := asString(firstOf(sp.attrs, "gen_ai.response.model", "gen_ai.request.model")); m != "" {
			b.s.Model = m
		}
		op := asString(sp.attrs["gen_ai.operation.name"])
		if op == "execute_tool" || (sp.attrs["gen_ai.tool.name"] != nil && sp.attrs["gen_ai.input.messages"] == nil) {
			otlpTool(b, sp)
		} else {
			b.addMessages(otlpMessages(sp, "input"))
			b.addMessages(otlpMessages(sp, "output"))
		}
		if sp.statusCode == 2 {
			b.s.Outcome.Status = OutcomeError
			msg := firstNonEmpty(sp.statusMessage, asString(sp.attrs["error.type"]), "status ERROR")
			if len(b.s.Outcome.Hints) < 5 {
				b.s.Outcome.Hints = append(b.s.Outcome.Hints, fmt.Sprintf("span %q: %s", sp.name, oneLine(msg, 200)))
			}
		}
	}
	if b.s.Outcome.Status == "" && rootOK {
		b.s.Outcome.Status = OutcomeOK
		b.s.Outcome.Hints = append(b.s.Outcome.Hints, "root span status OK")
	}
	return b.s
}

// otlpTool records one execute_tool span.
func otlpTool(b *builder, sp otlpSpan) {
	name := asString(sp.attrs["gen_ai.tool.name"])
	if name == "" {
		name = strings.TrimSpace(strings.TrimPrefix(sp.name, "execute_tool"))
	}
	errText := ""
	if sp.statusCode == 2 {
		errText = firstNonEmpty(sp.statusMessage, asString(sp.attrs["error.type"]), "tool span status ERROR")
	}
	b.toolSpan(asString(sp.attrs["gen_ai.tool.call.id"]), name, sp.attrs["gen_ai.tool.call.arguments"],
		sp.attrs["gen_ai.tool.call.result"], errText, sp.statusCode == 2, sp.start)
}

// otlpMessages reads the input or output messages of an LLM span from
// whichever convention the instrumentation used.
func otlpMessages(sp otlpSpan, side string) []message {
	if raw := sp.attrs["gen_ai."+side+".messages"]; raw != nil {
		return stamp(parseMessages(jsonValue(raw)), sp.start)
	}
	prefix := "gen_ai.prompt."
	if side == "output" {
		prefix = "gen_ai.completion."
	}
	if msgs := indexedMessages(sp.attrs, prefix); len(msgs) > 0 {
		return stamp(msgs, sp.start)
	}
	var out []message
	for _, ev := range sp.events {
		role, ok := eventRole(ev.name)
		if !ok || (role == "assistant") != (side == "output") {
			continue
		}
		obj := map[string]any{"role": role}
		if body := asMap(jsonValue(ev.attrs["gen_ai.event.content"])); body != nil {
			for k, v := range body {
				obj[k] = v
			}
		}
		if msg := asMap(jsonValue(ev.attrs["message"])); msg != nil {
			for k, v := range msg {
				obj[k] = v
			}
		}
		for _, k := range []string{"content", "tool_calls", "id"} {
			if v, ok := ev.attrs[k]; ok && obj[k] == nil {
				obj[k] = jsonValue(v)
			}
		}
		if role == "tool" && obj["tool_call_id"] == nil {
			obj["tool_call_id"] = obj["id"]
		}
		if m, ok := parseMessage(obj); ok {
			m.time = ev.time
			out = append(out, m)
		}
	}
	return out
}

// eventRole maps a GenAI span event name to the role of its message.
func eventRole(name string) (string, bool) {
	switch name {
	case "gen_ai.system.message":
		return "system", true
	case "gen_ai.user.message":
		return "user", true
	case "gen_ai.assistant.message", "gen_ai.choice":
		return "assistant", true
	case "gen_ai.tool.message":
		return "tool", true
	}
	return "", false
}

// indexedMessages rebuilds messages from gen_ai.prompt.<i>.<field> style
// attributes, including tool_calls.<j>.<field>.
func indexedMessages(attrs map[string]any, prefix string) []message {
	byIndex := map[int]map[string]any{}
	for k, v := range attrs {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		idx, field, ok := strings.Cut(rest, ".")
		i, err := strconv.Atoi(idx)
		if !ok || err != nil || i < 0 || i > MaxTurns {
			continue
		}
		m := byIndex[i]
		if m == nil {
			m = map[string]any{}
			byIndex[i] = m
		}
		if call, sub, ok := strings.Cut(field, "tool_calls."); ok && call == "" {
			j, f, _ := strings.Cut(sub, ".")
			calls := asMap(m["_calls"])
			if calls == nil {
				calls = map[string]any{}
				m["_calls"] = calls
			}
			c := asMap(calls[j])
			if c == nil {
				c = map[string]any{}
				calls[j] = c
			}
			c[f] = v
			continue
		}
		m[field] = v
	}
	idx := make([]int, 0, len(byIndex))
	for i := range byIndex {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	var out []message
	for _, i := range idx {
		m := byIndex[i]
		if calls := asMap(m["_calls"]); calls != nil {
			var list []any
			for j := 0; j < len(calls)+MaxToolCalls && len(list) < len(calls); j++ {
				if c := asMap(calls[strconv.Itoa(j)]); c != nil {
					list = append(list, c)
				}
			}
			m["tool_calls"] = list
			delete(m, "_calls")
		}
		if m["role"] == nil {
			m["role"] = "assistant"
			if prefix == "gen_ai.prompt." {
				m["role"] = "user"
			}
		}
		if msg, ok := parseMessage(m); ok {
			out = append(out, msg)
		}
	}
	return out
}

// stamp gives messages without a time of their own the span's start.
func stamp(msgs []message, t time.Time) []message {
	for i := range msgs {
		if msgs[i].time.IsZero() {
			msgs[i].time = t
		}
	}
	return msgs
}

// jsonValue decodes an attribute that holds JSON text; other values pass.
func jsonValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
		return v
	}
	var out any
	dec := json.NewDecoder(strings.NewReader(t))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return v
	}
	return out
}

// decodeSpan reads one span object.
func decodeSpan(obj map[string]any) otlpSpan {
	if obj == nil {
		return otlpSpan{}
	}
	sp := otlpSpan{
		traceID:  asString(field(obj, "traceId", "trace_id")),
		spanID:   asString(field(obj, "spanId", "span_id")),
		parentID: asString(field(obj, "parentSpanId", "parent_span_id")),
		name:     asString(obj["name"]),
		start:    parseTime(field(obj, "startTimeUnixNano", "start_time_unix_nano")),
		end:      parseTime(field(obj, "endTimeUnixNano", "end_time_unix_nano")),
		attrs:    attributes(obj["attributes"]),
	}
	for _, e := range asArray(obj["events"]) {
		eo := asMap(e)
		sp.events = append(sp.events, otlpEvent{name: asString(eo["name"]), time: parseTime(field(eo, "timeUnixNano", "time_unix_nano")), attrs: attributes(eo["attributes"])})
	}
	if st := asMap(obj["status"]); st != nil {
		sp.statusMessage = asString(st["message"])
		switch c := st["code"].(type) {
		case json.Number:
			n, _ := c.Int64()
			sp.statusCode = int(n)
		case string:
			switch c {
			case "STATUS_CODE_OK", "OK", "1":
				sp.statusCode = 1
			case "STATUS_CODE_ERROR", "ERROR", "2":
				sp.statusCode = 2
			}
		}
	}
	return sp
}

// attributes flattens an OTLP KeyValue list to a map of plain values.
func attributes(v any) map[string]any {
	out := map[string]any{}
	for _, kv := range asArray(v) {
		obj := asMap(kv)
		if k := asString(obj["key"]); k != "" {
			out[k] = anyValue(asMap(obj["value"]))
		}
	}
	return out
}

// anyValue decodes an OTLP AnyValue. Integers arrive as strings or numbers.
func anyValue(v map[string]any) any {
	switch {
	case v == nil:
		return nil
	case v["stringValue"] != nil:
		return asString(v["stringValue"])
	case v["boolValue"] != nil:
		return asBool(v["boolValue"])
	case v["intValue"] != nil:
		return json.Number(asString(v["intValue"]))
	case v["doubleValue"] != nil:
		return v["doubleValue"]
	case v["arrayValue"] != nil:
		var out []any
		for _, x := range asArray(asMap(v["arrayValue"])["values"]) {
			out = append(out, anyValue(asMap(x)))
		}
		return out
	case v["kvlistValue"] != nil:
		return attributes(asMap(v["kvlistValue"])["values"])
	case v["bytesValue"] != nil:
		return asString(v["bytesValue"])
	}
	return nil
}

// field returns the first of the given keys that obj holds.
func field(obj map[string]any, names ...string) any {
	if obj == nil {
		return nil
	}
	return firstOf(obj, names...)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
