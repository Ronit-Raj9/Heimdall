package traceimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/traceexport"
)

func TestParseOTLP(t *testing.T) {
	tr, err := Parse(fixture(t, "otlp-support.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.ID != "5b8efff798038103d269b633813fc60c" || s.Format != FormatOTLP || s.Model != "gpt-4.1" || s.Name != "invoke_agent support" {
		t.Errorf("session = %s %s %s %q", s.ID, s.Format, s.Model, s.Name)
	}
	if got := s.Requests(); len(got) != 1 || !strings.HasPrefix(got[0], "Please refund order A-1001") {
		t.Errorf("requests = %q", got)
	}
	if len(s.Turns) != 3 || s.Skipped["image"] != 1 {
		t.Errorf("turns = %d (want system, user, assistant once each), skipped = %v", len(s.Turns), s.Skipped)
	}
	if len(s.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", s.ToolCalls)
	}
	look, refund := s.ToolCalls[0], s.ToolCalls[1]
	if look.Name != "lookup_order" || look.ID != "call_1" || string(look.Arguments) != `{"order_id":"A-1001"}` || !strings.Contains(look.Result, `"status":"delivered"`) || look.IsError {
		t.Errorf("lookup_order = %+v", look)
	}
	if refund.Name != "issue_refund" || !refund.IsError || !strings.Contains(refund.Error, "payment gateway timed out") || !strings.Contains(string(refund.Arguments), `"amount":249.0`) {
		t.Errorf("issue_refund = %+v", refund)
	}
	if s.FinalAnswer != "Your refund of 249.00 for order A-1001 has been issued." {
		t.Errorf("final answer = %q", s.FinalAnswer)
	}
	if s.Outcome.Status != OutcomeError || len(s.Outcome.Hints) != 3 || s.Summary() != "error (1 tool errors)" {
		t.Errorf("outcome = %+v, summary %q", s.Outcome, s.Summary())
	}
	if s.Started.IsZero() || !s.Ended.After(s.Started) {
		t.Errorf("times %v %v", s.Started, s.Ended)
	}
	legacy := tr.Sessions[1]
	if legacy.FirstRequest() != "What is the return window for tents?" || legacy.FinalAnswer != "Tents can be returned within 30 days." ||
		legacy.Outcome.Status != OutcomeOK || legacy.Model != "gpt-4o-mini" {
		t.Errorf("indexed attributes session = %+v", legacy)
	}
}

func TestOTLPEventsAndIndexedToolCalls(t *testing.T) {
	data := `{"resourceSpans":[{"scopeSpans":[{"spans":[
	 {"traceId":"t1","spanId":"s1","name":"chat","startTimeUnixNano":"1763190000000000000","attributes":[
	   {"key":"gen_ai.prompt.0.role","value":{"stringValue":"user"}},
	   {"key":"gen_ai.prompt.0.content","value":{"stringValue":"Book a table for two."}},
	   {"key":"gen_ai.completion.0.tool_calls.0.id","value":{"stringValue":"k1"}},
	   {"key":"gen_ai.completion.0.tool_calls.0.name","value":{"stringValue":"book"}},
	   {"key":"gen_ai.completion.0.tool_calls.0.arguments","value":{"stringValue":"{\"people\":2}"}}]},
	 {"traceId":"t1","spanId":"s2","name":"chat","startTimeUnixNano":"1763190001000000000","attributes":[],
	  "events":[
	   {"name":"gen_ai.tool.message","timeUnixNano":"1763190001000000000","attributes":[{"key":"id","value":{"stringValue":"k1"}},{"key":"content","value":{"stringValue":"booked at 19:00"}}]},
	   {"name":"gen_ai.choice","timeUnixNano":"1763190002000000000","attributes":[{"key":"message","value":{"stringValue":"{\"content\":\"Booked for 19:00.\"}"}}]}]}
	]}]}]}`
	tr, err := Parse([]byte(data), FormatOTLP)
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if len(s.ToolCalls) != 1 || s.ToolCalls[0].Name != "book" || s.ToolCalls[0].Result != "booked at 19:00" || string(s.ToolCalls[0].Arguments) != `{"people":2}` {
		t.Errorf("tool calls = %+v", s.ToolCalls)
	}
	if s.FirstRequest() != "Book a table for two." || s.FinalAnswer != "Booked for 19:00." {
		t.Errorf("request %q answer %q", s.FirstRequest(), s.FinalAnswer)
	}
}

func TestAnyValue(t *testing.T) {
	attrs := attributes([]any{
		map[string]any{"key": "a", "value": map[string]any{"arrayValue": map[string]any{"values": []any{map[string]any{"stringValue": "x"}}}}},
		map[string]any{"key": "b", "value": map[string]any{"kvlistValue": map[string]any{"values": []any{map[string]any{"key": "k", "value": map[string]any{"boolValue": true}}}}}},
		map[string]any{"key": "c", "value": nil},
	})
	if arr, ok := attrs["a"].([]any); !ok || arr[0] != "x" {
		t.Errorf("array = %#v", attrs["a"])
	}
	if kv, ok := attrs["b"].(map[string]any); !ok || kv["k"] != true {
		t.Errorf("kvlist = %#v", attrs["b"])
	}
	if attrs["c"] != nil {
		t.Errorf("nil value = %#v", attrs["c"])
	}
}

// TestOTLPFromTraceExportTypes reads a trace written with the trace
// exporter's OTLP types, so a span shaped by `bifrost-bench export otlp`
// parses here too.
func TestOTLPFromTraceExportTypes(t *testing.T) {
	msgs := `[{"role":"user","parts":[{"type":"text","content":"Rename the file."}]}]`
	out := `[{"role":"assistant","parts":[{"type":"text","content":"Renamed."}]}]`
	doc := traceexport.TracesData{ResourceSpans: []traceexport.ResourceSpans{{ScopeSpans: []traceexport.ScopeSpans{{Spans: []traceexport.Span{
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7", Name: "chat m", StartTimeUnixNano: 1763190000000000000,
			Attributes: []traceexport.KeyValue{traceexport.Str("gen_ai.operation.name", "chat"), traceexport.Str("gen_ai.request.model", "m"),
				traceexport.Int("gen_ai.usage.input_tokens", 12), traceexport.Str("gen_ai.input.messages", msgs), traceexport.Str("gen_ai.output.messages", out)}},
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b8", ParentSpanID: "00f067aa0ba902b7", Name: "execute_tool mv", StartTimeUnixNano: 1763190001000000000,
			Attributes: []traceexport.KeyValue{traceexport.Str("gen_ai.operation.name", "execute_tool"), traceexport.Str("gen_ai.tool.name", "mv"),
				traceexport.Str("gen_ai.tool.call.arguments", `{"from":"a","to":"b"}`), traceexport.Str("gen_ai.tool.call.result", "ok")},
			Status: &traceexport.Status{Code: traceexport.StatusError, Message: "permission denied"}},
	}}}}}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Parse(b, "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.FirstRequest() != "Rename the file." || s.FinalAnswer != "Renamed." || len(s.ToolCalls) != 1 || !s.ToolCalls[0].IsError || s.Model != "m" {
		t.Errorf("session = %+v", s)
	}
}
