package traceinbox

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBifrostSessionResponsesRow: a Responses API row (Codex CLI) has no
// input_history; its conversation is in responses_input_history and
// responses_output. Before, the session was the content summary (the system
// instructions) labelled as the user turn, with no tool call (live lane l1).
func TestBifrostSessionResponsesRow(t *testing.T) {
	raw := `{"id":"row1","status":"error","provider":"openai","model":"gpt-6.2-sol",
	"content_summary":"You are a coding agent running in the Codex CLI.",
	"responses_input_history":[
	 {"type":"message","role":"developer","content":[{"type":"input_text","text":"You are a coding agent running in the Codex CLI."}]},
	 {"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the rounding bug in ledger/money.py."}]},
	 {"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]},
	 {"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"pytest -q\"}"},
	 {"type":"function_call_output","call_id":"call_1","output":"1 failed"},
	 {"type":"custom_tool_call","call_id":"call_2","name":"apply_patch","input":"*** Begin Patch"},
	 {"type":"custom_tool_call_output","call_id":"call_2","output":"Done"}
	],
	"responses_output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Fixed; all tests pass."}]}]}`
	var r bifrostRow
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	s, err := bifrostSession("r_x", &r)
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, tr := range s.Turns {
		roles = append(roles, tr.Role)
	}
	if len(s.Turns) < 3 || s.Turns[0].Role != "system" || s.Turns[1].Role != "user" || !strings.Contains(s.Turns[1].Text, "rounding bug") {
		t.Fatalf("turns %v: want the instructions as system, then the user task", roles)
	}
	if len(s.ToolCalls) != 2 || s.ToolCalls[0].Name != "exec_command" || s.ToolCalls[1].Name != "apply_patch" {
		t.Fatalf("tool calls %+v", s.ToolCalls)
	}
	if !strings.Contains(s.ToolCalls[0].Result, "1 failed") {
		t.Fatalf("tool result not joined: %+v", s.ToolCalls[0])
	}
	if !strings.Contains(s.FinalAnswer, "all tests pass") {
		t.Fatalf("final answer %q", s.FinalAnswer)
	}
}
