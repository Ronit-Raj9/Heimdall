package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func parseFixture(t *testing.T, name string) *schemas.ParseResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := fixture(t, commandAgent())
	rc.Agent.Gateway.Provider = "anthropic"
	res, err := newClaudeCodeParser().Parse(context.Background(), rc, raw)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestClaudeCodeParseFixture(t *testing.T) {
	res := parseFixture(t, "claudecode-stream.jsonl")

	counts := countEvents(res.Events)
	want := map[schemas.EventType]int{
		schemas.EventModelResponse: 6, // six message ids; msg_01AbCdEf spans two lines
		schemas.EventToolCall:      5,
		schemas.EventToolResult:    5,
		schemas.EventRetry:         1,
	}
	for typ, n := range want {
		if counts[typ] != n {
			t.Errorf("%s events = %d, want %d (all: %v)", typ, counts[typ], n, counts)
		}
	}
	if counts[schemas.EventError] != 0 {
		t.Errorf("unexpected error events: %d", counts[schemas.EventError])
	}

	for i, e := range res.Events {
		if e.Seq != i+1 || e.EventID != "r_p20fj3s8wa:agent_stream:"+strconv.Itoa(i+1) {
			t.Errorf("event %d: seq %d id %s", i, e.Seq, e.EventID)
		}
		if e.RunID != "r_p20fj3s8wa" || e.SlotID != "s_1" || e.PairID != "p_1" ||
			e.Source != schemas.SourceAgentStream || e.Evidence != schemas.EvidenceNative {
			t.Errorf("event %d identity wrong: %+v", i, e)
		}
	}

	calls := map[string]schemas.Event{}
	results := map[string]schemas.Event{}
	byID := map[string]schemas.Event{}
	for _, e := range res.Events {
		byID[e.EventID] = e
		switch e.Type {
		case schemas.EventToolCall:
			calls[e.ToolCallID] = e
		case schemas.EventToolResult:
			results[e.ToolCallID] = e
		}
	}
	targets := map[string]struct{ name, target string }{
		"toolu_01Read":  {"Read", "/workspace/ledger/money.py"},
		"toolu_01Test1": {"Bash", "python -m pytest -q"},
		"toolu_01Edit":  {"Edit", "/workspace/ledger/money.py"},
		"toolu_01Grep":  {"Grep", "/workspace"},
		"toolu_01Test2": {"Bash", "python -m pytest -q"},
	}
	for id, w := range targets {
		c, ok := calls[id]
		if !ok {
			t.Errorf("no tool_call %s", id)
			continue
		}
		if c.ToolName != w.name || c.Target != w.target {
			t.Errorf("%s: name %q target %q, want %q %q", id, c.ToolName, c.Target, w.name, w.target)
		}
		if len(c.ArgsHash) != 64 {
			t.Errorf("%s: args hash %q", id, c.ArgsHash)
		}
		parent := byID[c.ParentEventID]
		if parent.Type != schemas.EventModelResponse || parent.TurnIndex != c.TurnIndex {
			t.Errorf("%s: parent %q is not its turn's model_response", id, c.ParentEventID)
		}
		r := results[id]
		if r.ParentEventID != c.EventID || r.ToolName != w.name || r.TurnIndex != c.TurnIndex || len(r.ResultHash) != 64 {
			t.Errorf("%s: result not linked to its call: %+v", id, r)
		}
		if r.IsError != (id == "toolu_01Test1") {
			t.Errorf("%s: is_error = %v", id, r.IsError)
		}
	}
	if calls["toolu_01Test1"].ArgsHash == calls["toolu_01Test2"].ArgsHash {
		t.Error("different Bash inputs hash the same")
	}
	if !strings.Contains(results["toolu_01Grep"].Summary, "Found 2 files") {
		t.Errorf("block-array tool_result summary = %q", results["toolu_01Grep"].Summary)
	}

	first := res.Events[0]
	if first.Type != schemas.EventModelResponse || first.TurnIndex != 1 || first.ModelServed != "claude-sonnet-4-5-20250929" ||
		first.Provider != "anthropic" || first.ModelReq != "claude-sonnet-4-5" ||
		first.TS.Format(time.RFC3339Nano) != "2026-11-14T09:30:03.007Z" {
		t.Errorf("first model_response = %+v", first)
	}
	// Streamed lines carry no stop reason and running usage: tokens stay
	// unobserved rather than recording message_start counts.
	for _, e := range res.Events {
		if e.Tokens != nil {
			t.Errorf("event %s has tokens from a streamed line: %+v", e.EventID, e.Tokens)
		}
	}

	for _, e := range res.Events {
		if e.Type == schemas.EventRetry {
			if e.AttemptNo == nil || *e.AttemptNo != 1 || !e.IsError || !strings.Contains(e.Summary, "overloaded (529)") {
				t.Errorf("retry event = %+v", e)
			}
		}
	}

	c := res.Claim
	if c == nil || !c.Made || c.Matched != "all tests pass" || c.Source != schemas.EvidenceNative || c.Text != "All tests pass" {
		t.Errorf("claim = %+v", c)
	}
}

func TestClaudeCodeParseErrorsAndEdges(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantCounts map[schemas.EventType]int
		wantClaim  *bool
		wantMatch  string
		wantTokens *schemas.Tokens
	}{
		{
			name:       "empty output",
			raw:        "",
			wantCounts: map[schemas.EventType]int{},
		},
		{
			name:       "garbage only",
			raw:        "npm WARN something\n{not json\n[1,2]\n{\"type\":\"mystery\"}\n",
			wantCounts: map[schemas.EventType]int{},
		},
		{
			name: "synthetic api error then error result",
			raw: `{"type":"assistant","message":{"id":"x","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"API Error: 529 {\"type\":\"error\"}"}]},"error":"overloaded","parent_tool_use_id":null,"session_id":"s"}
{"type":"result","subtype":"success","is_error":true,"num_turns":1,"result":"API Error: 529 overloaded","session_id":"s"}`,
			wantCounts: map[schemas.EventType]int{schemas.EventError: 2},
			wantClaim:  ptr(false),
		},
		{
			name: "error result with errors list",
			raw: `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"Working on it."}]},"parent_tool_use_id":null}
{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":60,"errors":["Reached maximum number of turns (60)"]}`,
			wantCounts: map[schemas.EventType]int{schemas.EventModelResponse: 1, schemas.EventError: 1},
			wantClaim:  ptr(false),
		},
		{
			name:       "claim from last assistant text when no result",
			raw:        `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"I fixed the parser and the tests pass now."}]},"parent_tool_use_id":null}`,
			wantCounts: map[schemas.EventType]int{schemas.EventModelResponse: 1},
			wantClaim:  ptr(true),
			wantMatch:  "tests pass",
		},
		{
			name: "subagent text never becomes the claim",
			raw: `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"I could not reproduce the bug."}]},"parent_tool_use_id":null}
{"type":"assistant","message":{"id":"m2","model":"claude-sonnet-4-5","content":[{"type":"text","text":"Done, all tests pass."}]},"parent_tool_use_id":"toolu_task1"}`,
			wantCounts: map[schemas.EventType]int{schemas.EventModelResponse: 2},
			wantClaim:  ptr(false),
		},
		{
			name: "compaction and string content user line",
			raw: `{"type":"user","message":{"role":"user","content":"Fix the bug."},"parent_tool_use_id":null}
{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":150000}}
{"type":"result","subtype":"success","is_error":false,"result":"Implemented the CSV export."}`,
			wantCounts: map[schemas.EventType]int{schemas.EventCompaction: 1},
			wantClaim:  ptr(true),
			wantMatch:  "implemented",
		},
		{
			name:       "tool result for an unknown call",
			raw:        `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_orphan","content":"boom","is_error":true}]}}`,
			wantCounts: map[schemas.EventType]int{schemas.EventToolResult: 1},
			wantClaim:  ptr(false),
		},
		{
			name:       "complete message records tokens",
			raw:        `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"Hi."}],"stop_reason":"end_turn","usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":5}}}`,
			wantCounts: map[schemas.EventType]int{schemas.EventModelResponse: 1},
			wantClaim:  ptr(false),
			wantTokens: &schemas.Tokens{Fresh: 10, CacheRead: 20, CacheWrite: 30, Out: 5},
		},
		{
			name: "error result text is not the agent's claim",
			raw: `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"Running the tests."}]}}
{"type":"result","subtype":"success","is_error":true,"result":"API Error: 500 the request is done but failed upstream"}`,
			wantCounts: map[schemas.EventType]int{schemas.EventModelResponse: 1, schemas.EventError: 1},
			wantClaim:  ptr(false),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := newClaudeCodeParser().Parse(context.Background(), &schemas.RunContext{Run: &schemas.Run{RunID: "r_x"}}, []byte(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			got := countEvents(res.Events)
			if len(got) != len(tt.wantCounts) {
				t.Errorf("events = %v, want %v", got, tt.wantCounts)
			}
			for typ, n := range tt.wantCounts {
				if got[typ] != n {
					t.Errorf("%s = %d, want %d (all %v)", typ, got[typ], n, got)
				}
			}
			switch {
			case tt.wantClaim == nil && res.Claim != nil:
				t.Errorf("claim = %+v, want none", res.Claim)
			case tt.wantClaim != nil && (res.Claim == nil || res.Claim.Made != *tt.wantClaim):
				t.Errorf("claim = %+v, want made=%v", res.Claim, *tt.wantClaim)
			case tt.wantMatch != "" && res.Claim.Matched != tt.wantMatch:
				t.Errorf("matched = %q, want %q", res.Claim.Matched, tt.wantMatch)
			}
			if tt.wantTokens != nil && (len(res.Events) == 0 || res.Events[0].Tokens == nil || *res.Events[0].Tokens != *tt.wantTokens) {
				t.Errorf("tokens = %+v, want %+v", res.Events, *tt.wantTokens)
			}
		})
	}
}

func TestClaimFromText(t *testing.T) {
	tests := []struct {
		text    string
		made    bool
		matched string
	}{
		{"All tests pass.", true, "all tests pass"},
		{"All the tests passed after the change.", true, "all the tests passed"},
		{"I fixed the rounding bug.", true, "fixed"},
		{"Done.", true, "done"},
		{"The migration is completed.", true, "completed"},
		{"Implemented CSV output for the export command.", true, "implemented"},
		{"The importer now successfully accepts day-first dates.", true, "successfully"},
		{"Tests pass locally", true, "tests pass"},
		{"I looked at the code. Fixed the window boundary.\nAll 14 tests pass.", true, "all 14 tests pass"},
		{"Fixed the parser. All tests pass.", true, "all tests pass"},
		{"The model is unavailable, so I could not finish.", false, ""},
		{"I can't complete this task.", false, ""},
		{"I couldn't get the tests to pass.", false, ""},
		{"The fix is not done.", false, ""},
		{"I was unable to fix the bug.", false, ""},
		{"Tests failed, nothing is fixed.", false, ""},
		{"The bug isn’t fixed yet.", false, ""},
		{"Here is my analysis of the problem.", false, ""},
		{"I prefixed the names.", false, ""},
		{"I abandoned the approach.", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			c := ClaimFromText(tt.text)
			if c == nil {
				t.Fatal("nil claim for non-empty text")
			}
			if c.Made != tt.made || c.Matched != tt.matched || c.Source != schemas.EvidenceOutcome || c.Text == "" {
				t.Fatalf("claim = %+v, want made=%v matched=%q", c, tt.made, tt.matched)
			}
		})
	}
	if ClaimFromText("  \n ") != nil {
		t.Error("blank text should give no claim")
	}
	long := strings.Repeat("word ", 200) + "and the end"
	if c := ClaimFromText(long); len([]rune(c.Text)) != claimTextMax || !strings.HasSuffix(c.Text, "and the end") {
		t.Errorf("long text excerpt = %d runes, %q", len([]rune(c.Text)), c.Text)
	}
}

func TestArgsHashIsCanonical(t *testing.T) {
	a := hashRaw(json.RawMessage(`{"command":"ls -la","description":"list"}`))
	b := hashRaw(json.RawMessage("{ \"description\": \"list\",\n \"command\": \"ls -la\" }"))
	if a != b {
		t.Fatalf("key order or whitespace changed the hash: %s vs %s", a, b)
	}
	want, err := utils.HashJSON(map[string]any{"command": "ls -la", "description": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if a != want {
		t.Fatalf("hash %s, want utils.HashJSON %s", a, want)
	}
}

func ptr[T any](v T) *T { return &v }

// TestClaudeCodeParseRealOutput reads real Claude Code 2.1.325 output,
// captured from `claude -p --dangerously-skip-permissions --output-format
// stream-json --verbose` in bench/agent-claude-code:1 against a scripted fake
// Messages API: one healthy run (two failing tool calls, then a claim) and one
// where every call returns 529 (CLAUDE_CODE_MAX_RETRIES=2).
func TestClaudeCodeParseRealOutput(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		res := parseFixture(t, "claudecode-2.1.325-fakeapi.jsonl")
		got := countEvents(res.Events)
		want := map[schemas.EventType]int{schemas.EventModelResponse: 3, schemas.EventToolCall: 2, schemas.EventToolResult: 2}
		if len(got) != len(want) {
			t.Errorf("events = %v, want %v", got, want)
		}
		for typ, n := range want {
			if got[typ] != n {
				t.Errorf("%s = %d, want %d", typ, got[typ], n)
			}
		}
		for _, e := range res.Events {
			if e.Type == schemas.EventToolResult && !e.IsError {
				t.Errorf("tool result %s should be an error", e.ToolCallID)
			}
			if e.Type == schemas.EventToolCall && e.Target == "" {
				t.Errorf("tool call %s has no target", e.ToolCallID)
			}
		}
		if c := res.Claim; c == nil || !c.Made || c.Matched != "all tests pass" {
			t.Errorf("claim = %+v", c)
		}
	})
	t.Run("outage", func(t *testing.T) {
		res := parseFixture(t, "claudecode-2.1.325-outage.jsonl")
		got := countEvents(res.Events)
		if got[schemas.EventRetry] != 2 || got[schemas.EventError] != 2 || got[schemas.EventModelResponse] != 0 {
			t.Errorf("events = %v, want 2 retries, 2 errors, no model turn", got)
		}
		if c := res.Claim; c == nil || c.Made {
			t.Errorf("claim = %+v, want an observed non-claim", c)
		}
	})
}
