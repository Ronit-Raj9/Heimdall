package traceimport

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAddMessagesCountsRepeats(t *testing.T) {
	b := newBuilder("s")
	u := message{role: "user", text: "yes"}
	a := message{role: "assistant", text: "Shall I?"}
	b.addMessages([]message{u})
	b.addMessages([]message{u, a})
	b.addMessages([]message{u, a, u})
	if len(b.s.Turns) != 3 {
		t.Fatalf("turns = %+v, want user, assistant, user: a repeated message in a longer history is new", b.s.Turns)
	}
}

func TestToolCallMatching(t *testing.T) {
	b := newBuilder("s")
	at := time.Unix(1763190000, 0)
	b.requestCall(callPart{id: "1", name: "a", args: `{"x":1}`}, at)
	b.requestCall(callPart{id: "1", name: "a"}, at)
	b.requestCall(callPart{name: "b", args: "not json"}, at)
	b.resultCall(resultPart{name: "b", result: map[string]any{"error": "boom"}}, at)
	b.toolSpan("1", "a", nil, "done", "", false, at)
	b.resultCall(resultPart{id: "9", name: "c", result: "orphan"}, at)
	calls := b.s.ToolCalls
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Result != "done" || string(calls[0].Arguments) != `{"x":1}` {
		t.Errorf("call 1 = %+v", calls[0])
	}
	if string(calls[1].Arguments) != `"not json"` || !calls[1].IsError {
		t.Errorf("call 2 = %+v", calls[1])
	}
	if calls[2].Name != "c" || calls[2].Result != "orphan" {
		t.Errorf("call 3 = %+v", calls[2])
	}
}

func TestResultText(t *testing.T) {
	tests := []struct {
		in    any
		text  string
		isErr bool
	}{
		{"plain", "plain", false},
		{"Error: no such item", "Error: no such item", true},
		{map[string]any{"content": []any{map[string]any{"type": "text", "text": "hi"}}, "isError": true}, "hi", true},
		{`{"error":null,"ok":true}`, `{"error":null,"ok":true}`, false},
		{json.Number("3"), "3", false},
	}
	for _, tc := range tests {
		text, isErr := resultText(tc.in)
		if text != tc.text || isErr != tc.isErr {
			t.Errorf("resultText(%v) = %q, %v", tc.in, text, isErr)
		}
	}
}

func TestParseTime(t *testing.T) {
	want := time.Unix(1763190000, 0).UTC()
	for _, v := range []any{"2025-11-15T07:00:00Z", json.Number("1763190000"), json.Number("1763190000000"), "1763190000000000000"} {
		if got := parseTime(v); !got.Equal(want) {
			t.Errorf("parseTime(%v) = %v", v, got)
		}
	}
	if !parseTime("yesterday").IsZero() || !parseTime(json.Number("-5")).IsZero() {
		t.Error("garbage time was parsed")
	}
}
