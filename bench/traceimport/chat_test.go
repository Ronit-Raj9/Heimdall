package traceimport

import (
	"testing"
)

func TestParseChat(t *testing.T) {
	tr, err := Parse(fixture(t, "chat-transcript.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.ID != "conv-42" || s.Model != "gpt-4.1-mini" || s.Outcome.Status != OutcomeError {
		t.Errorf("session = %s %s %+v", s.ID, s.Model, s.Outcome)
	}
	if len(s.ToolCalls) != 2 || s.ToolCalls[1].Name != "adjust_stock" || string(s.ToolCalls[1].Arguments) != `{"delta":12,"reason":"delivery","sku":"W-101"}` {
		t.Errorf("tool calls = %+v", s.ToolCalls)
	}
	if s.FinalAnswer != "Done: recorded 12 units." || len(s.Turns) != 4 {
		t.Errorf("answer %q, %d turns", s.FinalAnswer, len(s.Turns))
	}
}

func TestParseChatShapes(t *testing.T) {
	tests := []struct {
		name, data string
		sessions   int
		first      string
	}{
		{"jsonl conversations", string(fixture(t, "chat-multi.jsonl")), 2, "What is 2 + 2?"},
		{"bare message array", `[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`, 1, "hi"},
		{"message per line", "{\"role\":\"user\",\"content\":\"a\"}\n{\"role\":\"assistant\",\"content\":\"b\"}\n", 1, "a"},
		{"array of conversations", `[{"messages":[{"role":"human","content":"x"}]},{"messages":[{"role":"user","content":"y"}]}]`, 2, "x"},
		{"data list", `{"data":[{"messages":[{"role":"user","content":"z"}]}]}`, 1, "z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := Parse([]byte(tc.data), FormatChat)
			if err != nil {
				t.Fatal(err)
			}
			if len(tr.Sessions) != tc.sessions || tr.Sessions[0].FirstRequest() != tc.first {
				t.Errorf("sessions = %d, first request %q", len(tr.Sessions), tr.Sessions[0].FirstRequest())
			}
		})
	}
}

func TestChatAnthropicBlocks(t *testing.T) {
	data := `{"messages":[
	 {"role":"user","content":[{"type":"text","text":"Weather in Oslo?"},{"type":"image","source":{}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"weather","input":{"city":"Oslo"}}]},
	 {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Error: upstream 503","is_error":true}]},
	 {"role":"assistant","content":"I could not get the weather."}],"error":"agent gave up"}`
	tr, err := Parse([]byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if len(s.ToolCalls) != 1 || !s.ToolCalls[0].IsError || s.ToolCalls[0].Result != "Error: upstream 503" || s.Skipped["image"] != 1 {
		t.Errorf("session = %+v", s)
	}
	if len(s.Requests()) != 1 || s.Outcome.Status != OutcomeError {
		t.Errorf("requests %q outcome %+v: a tool_result block is not a user request", s.Requests(), s.Outcome)
	}
}
