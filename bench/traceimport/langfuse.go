package traceimport

import (
	"fmt"
	"sort"
	"strings"
)

// This file reads Langfuse exports. Verified against Langfuse's public API
// reference: a trace with full details (GET /api/public/traces/{id}: id,
// name, timestamp, input, output, sessionId, scores and observations), a
// page of traces ({"data": [...], "meta": {...}}), and observation rows of
// the Observations API v2 ({"data": [...]} where each row has traceId, type,
// startTime), grouped by traceId. Each observation has id, type (GENERATION,
// SPAN, EVENT, AGENT, TOOL, CHAIN, RETRIEVER, EVALUATOR, EMBEDDING,
// GUARDRAIL), name, startTime, model, input, output, level (DEBUG, DEFAULT,
// WARNING, ERROR) and statusMessage. Input and output are free JSON whose
// shape depends on the integration; the importer reads OpenAI-style message
// lists and message objects, plain strings, and objects with one obvious
// text field. The UI's table export (CSV or JSON) has no documented column
// list, so it is read only when its rows look like the API's traces.

var langfuseTypes = map[string]bool{"GENERATION": true, "SPAN": true, "EVENT": true, "AGENT": true, "TOOL": true,
	"CHAIN": true, "RETRIEVER": true, "EVALUATOR": true, "EMBEDDING": true, "GUARDRAIL": true}

func isLangfuseTrace(x map[string]any) bool {
	return asArray(x["observations"]) != nil
}

func isLangfuseObservation(x map[string]any) bool {
	return asString(x["traceId"]) != "" && langfuseTypes[strings.ToUpper(asString(x["type"]))] && x["startTime"] != nil
}

// parseLangfuse collects traces and observation rows from every value and
// builds one session per trace, in the order traces first appear.
func parseLangfuse(values []any) ([]*Session, error) {
	var traces []map[string]any
	rows := map[string][]any{}
	var rowOrder []string
	var collect func(v any, depth int) error
	collect = func(v any, depth int) error {
		switch x := v.(type) {
		case map[string]any:
			switch {
			case isLangfuseTrace(x):
				traces = append(traces, x)
			case isLangfuseObservation(x):
				id := asString(x["traceId"])
				if _, ok := rows[id]; !ok {
					rowOrder = append(rowOrder, id)
				}
				rows[id] = append(rows[id], x)
			case asArray(x["data"]) != nil && depth == 0:
				return collect(x["data"], depth+1)
			default:
				return fmt.Errorf("an object that is neither a trace with observations nor an observation row (keys %s)", keys(x))
			}
		case []any:
			if depth > 1 {
				return fmt.Errorf("arrays nested %d deep", depth)
			}
			for _, item := range x {
				if err := collect(item, depth+1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("a top-level %s; want an object or an array", kindOf(v))
		}
		return nil
	}
	for _, v := range values {
		if err := collect(v, 0); err != nil {
			return nil, err
		}
	}
	for _, id := range rowOrder {
		traces = append(traces, map[string]any{"id": id, "observations": rows[id]})
	}
	out := make([]*Session, 0, len(traces))
	for i, tr := range traces {
		out = append(out, langfuseSession(tr, i))
	}
	return out, nil
}

// langfuseSession builds one session from one trace.
func langfuseSession(tr map[string]any, n int) *Session {
	id := asString(tr["id"])
	if id == "" {
		id = fmt.Sprintf("trace-%d", n+1)
	}
	b := newBuilder(id)
	b.s.Name = asString(tr["name"])
	b.s.Started = parseTime(tr["timestamp"])
	obs := asArray(tr["observations"])
	sort.SliceStable(obs, func(i, j int) bool {
		return parseTime(asMap(obs[i])["startTime"]).Before(parseTime(asMap(obs[j])["startTime"]))
	})
	for _, raw := range obs {
		langfuseObservation(b, asMap(raw))
	}
	if len(b.s.Requests()) == 0 {
		// No generation carried the user's message: the trace input is the
		// request, and it came first.
		var first []Turn
		for _, m := range ioMessages(tr["input"], "user") {
			if m.text != "" {
				first = append(first, Turn{Role: m.role, Text: b.clip(m.text), Time: b.s.Started})
			}
		}
		b.s.Turns = append(first, b.s.Turns...)
	}
	if out := tr["output"]; out != nil {
		b.addMessages(ioMessages(out, "assistant"))
		if text := ioText(out); text != "" {
			b.s.FinalAnswer = b.clip(text)
		}
	}
	for _, sc := range asArray(tr["scores"]) {
		s := asMap(sc)
		val := firstNonEmpty(asString(s["stringValue"]), textOf(s["value"]))
		if name := asString(s["name"]); name != "" && len(b.s.Outcome.Hints) < 8 {
			b.s.Outcome.Hints = append(b.s.Outcome.Hints, fmt.Sprintf("score %s = %s", oneLine(name, 60), oneLine(val, 60)))
		}
	}
	return b.s
}

// langfuseObservation adds one observation: a generation's messages, a tool
// observation's call, and an ERROR level as an outcome hint.
func langfuseObservation(b *builder, o map[string]any) {
	if o == nil {
		return
	}
	typ := strings.ToUpper(asString(o["type"]))
	start := parseTime(o["startTime"])
	level := strings.ToUpper(asString(o["level"]))
	name := asString(o["name"])
	switch {
	case typ == "TOOL":
		errText := ""
		if level == "ERROR" {
			errText = firstNonEmpty(asString(o["statusMessage"]), "observation level ERROR")
		}
		callID := asString(firstOf(asMapOrEmpty(o["metadata"]), "tool_call_id", "toolCallId", "call_id"))
		b.toolSpan(callID, name, o["input"], o["output"], errText, level == "ERROR", start)
	case typ == "GENERATION" || asString(o["model"]) != "":
		if m := asString(o["model"]); m != "" {
			b.s.Model = m
		}
		b.addMessages(stamp(ioMessages(o["input"], "user"), start))
		b.addMessages(stamp(ioMessages(o["output"], "assistant"), parseTime(firstOf(o, "endTime", "startTime"))))
	}
	if level == "ERROR" {
		b.s.Outcome.Status = OutcomeError
		if len(b.s.Outcome.Hints) < 5 {
			b.s.Outcome.Hints = append(b.s.Outcome.Hints, fmt.Sprintf("observation %q level ERROR: %s", name, oneLine(asString(o["statusMessage"]), 200)))
		}
	}
}

// ioMessages reads a Langfuse input or output value as messages: a message
// list, a message object, an object with choices or messages, or plain text
// in the given role.
func ioMessages(v any, role string) []message {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return parseMessages(x)
	case map[string]any:
		if asArray(x["messages"]) != nil {
			return parseMessages(x["messages"])
		}
		if choices := asArray(x["choices"]); choices != nil {
			var out []message
			for _, c := range choices {
				if m, ok := parseMessage(asMap(asMap(c)["message"])); ok {
					out = append(out, m)
				}
			}
			return out
		}
		if asString(x["role"]) != "" {
			if m, ok := parseMessage(x); ok {
				return []message{m}
			}
			return nil
		}
	}
	if text := ioText(v); text != "" {
		return []message{{role: role, text: strings.TrimSpace(text)}}
	}
	return nil
}

// ioText reads free-form input or output as text: a string, the one obvious
// text field of an object, else compact JSON.
func ioText(v any) string {
	if obj := asMap(v); obj != nil {
		for _, k := range []string{"output", "answer", "response", "content", "text", "query", "question", "input", "prompt", "message"} {
			if s, ok := obj[k].(string); ok {
				return s
			}
		}
	}
	return textOf(v)
}

func asMapOrEmpty(v any) map[string]any {
	if m := asMap(v); m != nil {
		return m
	}
	return map[string]any{}
}
