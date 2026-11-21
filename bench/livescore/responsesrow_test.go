package livescore

import (
	"encoding/json"
	"testing"
)

// TestTranscriptOfResponsesRow: a Responses API row (Codex CLI) has no
// input_history; its transcript comes from responses_input_history and
// responses_output, so tool signals see its tool calls (live lane l1: every
// Codex session had no transcript and tool_error_rate had 0 pairs).
func TestTranscriptOfResponsesRow(t *testing.T) {
	raw := `{"id":"row1","status":"success","provider":"openai","model":"gpt-6.2-sol",
	"responses_input_history":[
	 {"type":"message","role":"user","content":[{"type":"input_text","text":"Fix the bug."}]},
	 {"type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"cmd\":\"pytest -q\"}"},
	 {"type":"function_call_output","call_id":"call_1","output":"Error: pytest crashed"}
	],
	"responses_output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}]}`
	var r Row
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	s := transcriptOf("r_x", []Row{r})
	if s == nil {
		t.Fatal("no transcript from a Responses row")
	}
	if len(s.ToolCalls) != 1 || s.ToolCalls[0].Name != "exec_command" || s.FinalAnswer != "Done." {
		t.Fatalf("session %+v", s)
	}
	if hidden := (Row{ContentHidden: true, ResponsesInputHistory: r.ResponsesInputHistory}); transcriptOf("r_y", []Row{hidden}) != nil {
		t.Fatal("a content-hidden row gave a transcript")
	}
}
