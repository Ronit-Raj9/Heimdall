package traceimport

import (
	"strings"
	"testing"
)

func TestParseLangfuseTrace(t *testing.T) {
	tr, err := Parse(fixture(t, "langfuse-trace.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(tr.Sessions))
	}
	s := tr.Sessions[0]
	if s.ID != "lf-trace-7f3a" || s.Model != "gpt-4o-mini" || s.Name != "weather-assistant" {
		t.Errorf("session = %s %s %s", s.ID, s.Model, s.Name)
	}
	if len(s.Turns) != 3 || s.Turns[0].Role != "system" || s.Turns[1].Role != "user" || s.Turns[2].Role != "assistant" {
		t.Errorf("turns = %+v", s.Turns)
	}
	if len(s.ToolCalls) != 1 || s.ToolCalls[0].Name != "get_forecast" || s.ToolCalls[0].ID != "call_w1" || !strings.Contains(s.ToolCalls[0].Result, `"summary":"rain"`) {
		t.Errorf("tool calls = %+v (the TOOL observation and the generation's tool call are one call)", s.ToolCalls)
	}
	if s.FinalAnswer != "No umbrella needed: tomorrow in Zurich is sunny, 18 C." {
		t.Errorf("final answer = %q", s.FinalAnswer)
	}
	if s.Outcome.Status != OutcomeUnknown || len(s.Outcome.Hints) != 1 || s.Outcome.Hints[0] != "score user-feedback = 0" {
		t.Errorf("outcome = %+v", s.Outcome)
	}
}

func TestParseLangfuseObservationRows(t *testing.T) {
	tr, err := Parse(fixture(t, "langfuse-observations.json"), FormatLangfuse)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Sessions) != 2 || tr.Sessions[0].ID != "lf-a" || tr.Sessions[1].ID != "lf-b" {
		t.Fatalf("sessions = %+v", tr.Sessions)
	}
	b := tr.Sessions[1]
	if b.FirstRequest() != "Close ticket T-18." || len(b.ToolCalls) != 1 || !b.ToolCalls[0].IsError || b.ToolCalls[0].Error != "403 forbidden" {
		t.Errorf("lf-b = %+v", b)
	}
	if b.Outcome.Status != OutcomeError {
		t.Errorf("outcome = %+v", b.Outcome)
	}
	if a := tr.Sessions[0]; a.FinalAnswer != "Ticket T-17 is a duplicate of T-12." {
		t.Errorf("lf-a answer = %q", a.FinalAnswer)
	}
}

func TestLangfuseTraceInputOnly(t *testing.T) {
	data := `[{"id":"x1","input":"Where is my parcel?","output":{"answer":"It ships tomorrow."},"observations":[]}]`
	tr, err := Parse([]byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.FirstRequest() != "Where is my parcel?" || s.FinalAnswer != "It ships tomorrow." || len(s.Turns) != 2 {
		t.Errorf("session = %+v", s)
	}
}
