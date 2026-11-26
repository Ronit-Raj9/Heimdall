package traceimport

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestParseClaudeCodeFixture reads a hand-written Claude Code session: two
// user turns, one failed tool call, a subagent line skipped, thinking dropped.
func TestParseClaudeCodeFixture(t *testing.T) {
	tr, err := Parse(readFixture(t, "claudecode-small.jsonl"), "")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Format != FormatClaudeCode || len(tr.Sessions) != 1 {
		t.Fatalf("format %q, %d sessions", tr.Format, len(tr.Sessions))
	}
	s := tr.Sessions[0]
	if s.ID != "sess-cc-1" || s.Model != "claude-test-1" {
		t.Errorf("id %q model %q", s.ID, s.Model)
	}
	reqs := s.Requests()
	if want := []string{"Refund order A-1001 to the customer.", "Try order A-1002 instead."}; !reflect.DeepEqual(reqs, want) {
		t.Errorf("requests = %q, want %q", reqs, want)
	}
	if len(s.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", s.ToolCalls)
	}
	c := s.ToolCalls[0]
	if c.Name != "refund_order" || c.ID != "toolu_1" || !c.IsError || c.Result != "order not found" {
		t.Errorf("call = %+v", c)
	}
	var args map[string]any
	if json.Unmarshal(c.Arguments, &args) != nil || args["order_id"] != "A-1001" {
		t.Errorf("arguments = %s", c.Arguments)
	}
	if s.FinalAnswer != "I could not refund the order." {
		t.Errorf("final answer = %q", s.FinalAnswer)
	}
	if s.Skipped["subagent line"] != 1 || s.Skipped["thinking"] != 1 {
		t.Errorf("skipped = %v", s.Skipped)
	}
	for _, turn := range s.Turns {
		if strings.Contains(turn.Text, "subagent") || strings.Contains(turn.Text, "system-reminder") || strings.Contains(turn.Text, "meta line") {
			t.Errorf("turn kept %q", turn.Text)
		}
	}
	if s.Workspace == nil || s.Workspace.Cwd != "/home/tester/shop" || s.Workspace.Branch != "fix-refunds" {
		t.Errorf("workspace = %+v", s.Workspace)
	}
	if s.Outcome.Status != OutcomeUnknown || len(s.Outcome.Hints) == 0 {
		t.Errorf("outcome = %+v", s.Outcome)
	}
}

// TestParseClaudeCodeAPIError: a last assistant line that is an API error
// makes the outcome an error.
func TestParseClaudeCodeAPIError(t *testing.T) {
	data := `{"type":"user","sessionId":"e1","uuid":"u1","message":{"role":"user","content":"hello there"}}
{"type":"assistant","sessionId":"e1","uuid":"a1","isApiErrorMessage":true,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API Error: overloaded"}]}}
`
	tr, err := Parse([]byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.Outcome.Status != OutcomeError || s.Model != "" {
		t.Errorf("outcome %+v model %q", s.Outcome, s.Model)
	}
}

// TestParseCodexWrappedAndOld: the wrapped rollout and the older bare layout
// give the same session.
func TestParseCodexWrappedAndOld(t *testing.T) {
	var got []*Session
	for _, name := range []string{"codex-wrapped.jsonl", "codex-old.jsonl"} {
		tr, err := Parse(readFixture(t, name), "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tr.Format != FormatCodex || len(tr.Sessions) != 1 {
			t.Fatalf("%s: format %q, %d sessions", name, tr.Format, len(tr.Sessions))
		}
		got = append(got, tr.Sessions[0])
	}
	for i, s := range got {
		if s.ID != "sess-cx-1" {
			t.Errorf("%d: id %q", i, s.ID)
		}
		if r := s.Requests(); len(r) != 1 || r[0] != "Run the tests and fix the failing one." {
			t.Errorf("%d: requests %q", i, r)
		}
		if len(s.ToolCalls) != 2 {
			t.Fatalf("%d: calls %+v", i, s.ToolCalls)
		}
		sh, patch := s.ToolCalls[0], s.ToolCalls[1]
		if sh.Name != "shell" || !sh.IsError || !strings.Contains(string(sh.Arguments), `"make"`) {
			t.Errorf("%d: shell call %+v", i, sh)
		}
		if patch.Name != "apply_patch" || patch.IsError || string(patch.Arguments) != `{"input":"*** Begin Patch"}` || patch.Result != "Done" {
			t.Errorf("%d: patch call %+v args %s", i, patch, patch.Arguments)
		}
		if s.FinalAnswer != "Fixed the add function." {
			t.Errorf("%d: final %q", i, s.FinalAnswer)
		}
		if s.Skipped["environment context"] != 1 {
			t.Errorf("%d: skipped %v", i, s.Skipped)
		}
	}
	a, b := got[0], got[1]
	if !reflect.DeepEqual(a.Turns[0].Text, b.Turns[0].Text) || len(a.Turns) != len(b.Turns) || a.FinalAnswer != b.FinalAnswer {
		t.Errorf("layouts differ: %+v vs %+v", a.Turns, b.Turns)
	}
	if a.Model != "gpt-test-1" || a.Workspace == nil || a.Workspace.Commit != "abc123" || a.Workspace.RepoURL != "https://example.com/app.git" || a.Workspace.Cwd != "/Users/tester/app" {
		t.Errorf("model %q workspace %+v", a.Model, a.Workspace)
	}
}

// TestParseCodexAborted: turn_aborted adds a hint and makes the outcome an error.
func TestParseCodexAborted(t *testing.T) {
	data := `{"timestamp":"2026-11-21T09:00:00Z","type":"session_meta","payload":{"id":"ab1","cwd":"/tmp/x"}}
{"timestamp":"2026-11-21T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"do it"}]}}
{"timestamp":"2026-11-21T09:00:02Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}
`
	tr, err := Parse([]byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	s := tr.Sessions[0]
	if s.Outcome.Status != OutcomeError || !slicesHas(s.Outcome.Hints, "turn aborted: interrupted") {
		t.Errorf("outcome = %+v", s.Outcome)
	}
}

func slicesHas(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func TestDetectNewFormats(t *testing.T) {
	for name, want := range map[string]string{
		"claudecode-small.jsonl": FormatClaudeCode,
		"codex-wrapped.jsonl":    FormatCodex,
		"codex-old.jsonl":        FormatCodex,
		"chat-multi.jsonl":       FormatChat,
	} {
		got, err := Detect(readFixture(t, name))
		if err != nil || got != want {
			t.Errorf("Detect(%s) = %q, %v; want %q", name, got, err, want)
		}
	}
	formats := strings.Join(Formats(), ",")
	if !strings.Contains(formats, FormatClaudeCode) || !strings.Contains(formats, FormatCodex) {
		t.Errorf("Formats() = %s", formats)
	}
}

// TestSessionFileAsJSONArray: the lines of a session file sent as one JSON
// array (the inbox push body holds one JSON value) read the same way.
func TestSessionFileAsJSONArray(t *testing.T) {
	for _, name := range []string{"claudecode-small.jsonl", "codex-wrapped.jsonl"} {
		var lines []json.RawMessage
		for _, l := range strings.Split(strings.TrimSpace(string(readFixture(t, name))), "\n") {
			lines = append(lines, json.RawMessage(l))
		}
		arr, _ := json.Marshal(lines)
		direct, err := Parse(readFixture(t, name), "")
		if err != nil {
			t.Fatal(err)
		}
		tr, err := Parse(arr, "")
		if err != nil {
			t.Fatalf("%s as array: %v", name, err)
		}
		if tr.Format != direct.Format || len(tr.Sessions[0].Turns) != len(direct.Sessions[0].Turns) || len(tr.Sessions[0].ToolCalls) != len(direct.Sessions[0].ToolCalls) {
			t.Errorf("%s: array gives %+v, lines give %+v", name, tr.Sessions[0], direct.Sessions[0])
		}
		if _, err := Parse(arr, direct.Format); err != nil {
			t.Errorf("%s with the format named: %v", name, err)
		}
	}
}
