package schemas

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestAgentEventRecordRoundTrip(t *testing.T) {
	record := AgentEventRecord{Schema: AgentEventSchema, Version: AgentEventVersion, Type: EventToolResult,
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ToolCallID: "call1", ToolName: "shell",
		Status: EventStatusError, IsError: true, Summary: "command failed"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var got AgentEventRecord
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(record, got) {
		t.Fatalf("record=%+v err=%v", got, err)
	}
}

func TestTraceDiagnosticsRoundTripAndLegacy(t *testing.T) {
	want := &ParseDiagnostics{Source: TraceSourceProtocol, Quality: TraceQualityPartial, Parser: "protocol",
		ParserVersion: "1", Lines: 4, Recognized: 2, Emitted: 2, Malformed: 1, Oversized: 1, Truncated: true}
	data, err := json.Marshal(Run{Agent: RunAgentInfo{TraceSource: want.Source, TraceQuality: want.Quality, TraceDiagnostics: want}})
	if err != nil {
		t.Fatal(err)
	}
	var got Run
	if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(want, got.Agent.TraceDiagnostics) {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	var old Run
	if err := json.Unmarshal([]byte(`{"agent":{"parser":"codex"}}`), &old); err != nil || old.Agent.TraceDiagnostics != nil {
		t.Fatalf("legacy gained diagnostics: %+v err=%v", old, err)
	}
}
