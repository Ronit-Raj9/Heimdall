package traceimport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReferenceScript(t *testing.T) {
	s := &Session{ToolCalls: []ToolCall{
		{Seq: 1, Name: "get_item", Arguments: json.RawMessage(`{"sku":"W-1","note":"line1\nPY\nline3"}`), Result: "ok\nPY"},
		{Seq: 2, Name: "adjust", Arguments: json.RawMessage(`{}`), IsError: true, Error: "denied"},
	}}
	for _, tmpl := range []string{KindMCP, KindToolUse} {
		out := string(referenceScript(s, tmpl, "inventory", false))
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		for i, l := range lines[:len(lines)-1] {
			if l == "PY" {
				t.Errorf("%s: line %d ends the heredoc early", tmpl, i+1)
			}
		}
		if lines[len(lines)-1] != "PY" || !strings.HasPrefix(out, "#!/bin/sh\n# EDIT ME") {
			t.Errorf("%s: script frame:\n%s", tmpl, out)
		}
		if !strings.Contains(out, `tool("get_item", json.loads("{\"sku\":\"W-1\",\"note\":\"line1\\nPY\\nline3\"}"))`) || !strings.Contains(out, "# 2. adjust failed: denied") {
			t.Errorf("%s: calls:\n%s", tmpl, out)
		}
	}
	if !strings.Contains(string(referenceScript(s, KindToolUse, "inv", false)), "map tool") {
		t.Error("the tool-use reference does not refuse to run before tools are mapped")
	}
	if !strings.Contains(string(referenceScript(&Session{}, KindMCP, "x", false)), "made no tool calls") {
		t.Error("no note for a session without tool calls")
	}
}

func TestTranscriptAndConversation(t *testing.T) {
	s := &Session{ID: "t", Format: FormatChat, Outcome: Outcome{Status: OutcomeUnknown}, Skipped: map[string]int{"image": 2}, Truncated: 1,
		Turns:       []Turn{{Role: "system", Text: "sys"}, {Role: "user", Text: "first\n```\nfence"}, {Role: "assistant", Text: "a1"}, {Role: "user", Text: "last"}, {Role: "assistant", Text: "a2"}},
		FinalAnswer: "a2"}
	out := string(transcript(s, Source{File: "f.json", SHA256: "abc", Format: FormatChat}))
	for _, want := range []string{"- Redaction: off", "    first\n    ```\n    fence", "- Not imported: 1 fields truncated, 2 image", "## Final answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript lacks %q:\n%s", want, out)
		}
	}
	conv := string(conversationFile(s))
	if !strings.Contains(conv, "first") || !strings.Contains(conv, "a1") || strings.Contains(conv, "last") || strings.Contains(conv, "sys") {
		t.Errorf("conversation = %s", conv)
	}
}
