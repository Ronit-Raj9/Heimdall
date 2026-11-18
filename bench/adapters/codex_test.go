package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func parseCodex(t *testing.T, raw string) *schemas.ParseResult {
	t.Helper()
	agent := commandAgent()
	agent.Model = "gpt-5.6-sol"
	agent.Gateway.Provider = "openai"
	rc, _ := fixture(t, agent)
	res, err := newCodexParser().Parse(context.Background(), rc, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func codexFixture(t *testing.T, name string) *schemas.ParseResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return parseCodex(t, string(raw))
}

func eventsOf(events []schemas.Event, typ schemas.EventType) []schemas.Event {
	var out []schemas.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// TestCodexParseRealOutput reads real codex-cli 0.169.0 output, captured from
// `codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox
// --ephemeral --strict-config` in bench/agent-codex:1 with the shipped
// config.toml, against a scripted fake Responses API on an internal network:
//   - fakeapi: reasoning + message + pytest (exit 1), apply_patch, pytest
//     (exit 0), then a claim; four model requests.
//   - once503: the same script with the second request answered 503. Codex
//     retried it at the HTTP level and printed nothing about it.
//   - outage: every request from the second on answered 503. Codex made 30
//     attempts (6 stream attempts of 5 HTTP attempts each), printing
//     five "Reconnecting... n/5" lines, a final error and turn.failed; exit 1.
//   - edge: two tool calls in one response (run one after the other), an
//     apply_patch that fails verification (no line at all), a stream cut
//     before response.completed and a response.failed event, then a message
//     that admits failure.
func TestCodexParseRealOutput(t *testing.T) {
	cases := []struct {
		file    string
		counts  map[schemas.EventType]int
		claimed bool
		matched string
	}{
		{"codex-0.169.0-fakeapi.jsonl", map[schemas.EventType]int{
			schemas.EventModelResponse: 4, schemas.EventToolCall: 3, schemas.EventToolResult: 3}, true, "all tests pass"},
		{"codex-0.169.0-once503.jsonl", map[schemas.EventType]int{
			schemas.EventModelResponse: 4, schemas.EventToolCall: 3, schemas.EventToolResult: 3}, true, "all tests pass"},
		{"codex-0.169.0-outage.jsonl", map[schemas.EventType]int{
			schemas.EventModelResponse: 1, schemas.EventToolCall: 1, schemas.EventToolResult: 1,
			schemas.EventRetry: 5, schemas.EventError: 2}, false, ""},
		{"codex-0.169.0-edge.jsonl", map[schemas.EventType]int{
			schemas.EventModelResponse: 3, schemas.EventToolCall: 2, schemas.EventToolResult: 2,
			schemas.EventRetry: 2}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			res := codexFixture(t, tc.file)
			got := countEvents(res.Events)
			if len(got) != len(tc.counts) {
				t.Errorf("events = %v, want %v", got, tc.counts)
			}
			for typ, n := range tc.counts {
				if got[typ] != n {
					t.Errorf("%s = %d, want %d (all: %v)", typ, got[typ], n, got)
				}
			}
			for i, e := range res.Events {
				if e.Seq != i+1 || e.EventID != "r_p20fj3s8wa:agent_stream:"+strconv.Itoa(i+1) ||
					e.RunID != "r_p20fj3s8wa" || e.SlotID != "s_1" || e.PairID != "p_1" ||
					e.Source != schemas.SourceAgentStream || e.Evidence != schemas.EvidenceNative ||
					e.Provider != "openai" || e.ModelReq != "gpt-5.6-sol" {
					t.Errorf("event %d identity wrong: %+v", i, e)
				}
				if e.Tokens != nil {
					t.Errorf("event %d has tokens; Codex reports only the run total", i)
				}
			}
			c := res.Claim
			if c == nil || c.Made != tc.claimed || c.Matched != tc.matched || c.Source != schemas.EvidenceNative {
				t.Errorf("claim = %+v, want made %v matched %q", c, tc.claimed, tc.matched)
			}
		})
	}
}

func TestCodexParseHealthyDetail(t *testing.T) {
	res := codexFixture(t, "codex-0.169.0-fakeapi.jsonl")
	turns := eventsOf(res.Events, schemas.EventModelResponse)
	if len(turns) != 4 || turns[0].Summary != "Let me run the tests first." || turns[3].TurnIndex != 4 {
		t.Fatalf("turns = %+v", turns)
	}
	calls := eventsOf(res.Events, schemas.EventToolCall)
	results := eventsOf(res.Events, schemas.EventToolResult)
	want := []struct {
		id, name, target string
		turn             int
		isErr            bool
	}{
		{"item_2", "exec_command", "python -m pytest -q", 1, true},
		{"item_3", "apply_patch", "/workspace/money.py", 2, false},
		{"item_4", "exec_command", "python -m pytest -q", 3, false},
	}
	for i, w := range want {
		c, r := calls[i], results[i]
		if c.ToolCallID != w.id || c.ToolName != w.name || c.Target != w.target || c.TurnIndex != w.turn ||
			c.ParentEventID != turns[w.turn-1].EventID || c.ArgsHash == "" {
			t.Errorf("call %d = %+v", i, c)
		}
		if r.ToolCallID != w.id || r.ParentEventID != c.EventID || r.ToolName != w.name || r.Target != w.target ||
			r.IsError != w.isErr || r.ResultHash == "" || r.TurnIndex != w.turn {
			t.Errorf("result %d = %+v", i, r)
		}
	}
	h, err := utils.HashJSON(map[string]any{"command": "/bin/sh -c 'python -m pytest -q'"})
	if err != nil {
		t.Fatal(err)
	}
	if calls[0].ArgsHash != h || calls[0].ArgsHash != calls[2].ArgsHash {
		t.Errorf("args hash = %s / %s, want %s", calls[0].ArgsHash, calls[2].ArgsHash, h)
	}
	if results[0].ResultHash == results[2].ResultHash {
		t.Error("different outputs hashed the same")
	}
	if !strings.HasPrefix(results[0].Summary, "exit 1: 1 failed") {
		t.Errorf("summary = %q", results[0].Summary)
	}
}

func TestCodexParseOutageDetail(t *testing.T) {
	res := codexFixture(t, "codex-0.169.0-outage.jsonl")
	retries := eventsOf(res.Events, schemas.EventRetry)
	for i, r := range retries {
		if r.AttemptNo == nil || *r.AttemptNo != i+1 || !r.IsError ||
			!strings.HasPrefix(r.Summary, "reconnecting "+strconv.Itoa(i+1)+"/5: unexpected status 503") {
			t.Errorf("retry %d = %+v", i, r)
		}
	}
	errs := eventsOf(res.Events, schemas.EventError)
	if len(errs) != 2 || !strings.HasPrefix(errs[0].Summary, "error: unexpected status 503") ||
		!strings.HasPrefix(errs[1].Summary, "turn failed: unexpected status 503") || !errs[1].IsError {
		t.Errorf("errors = %+v", errs)
	}
	if res.Claim.Text != "Let me run the tests first." {
		t.Errorf("claim text = %q", res.Claim.Text)
	}
}

func TestCodexParseEdges(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		counts map[schemas.EventType]int
		check  func(t *testing.T, res *schemas.ParseResult)
	}{
		{
			name: "parallel calls in flight share a turn",
			raw: `{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"/bin/sh -c ls","status":"in_progress"}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/sh -c pwd","status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"/bin/sh -c ls","aggregated_output":"a\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/sh -c pwd","aggregated_output":"/w\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Done."}}`,
			counts: map[schemas.EventType]int{schemas.EventModelResponse: 2, schemas.EventToolCall: 2, schemas.EventToolResult: 2},
			check: func(t *testing.T, res *schemas.ParseResult) {
				calls := eventsOf(res.Events, schemas.EventToolCall)
				if calls[0].TurnIndex != 1 || calls[1].TurnIndex != 1 || calls[0].Target != "ls" || calls[1].Target != "pwd" {
					t.Errorf("calls = %+v", calls)
				}
				if res.Claim == nil || !res.Claim.Made || res.Claim.Matched != "done" {
					t.Errorf("claim = %+v", res.Claim)
				}
			},
		},
		{
			name: "completed without started, failed patch, mcp call, item error",
			raw: `{"type":"item.completed","item":{"id":"item_0","type":"file_change","changes":[{"path":"/workspace/a.py","kind":"update"},{"path":"/workspace/b.py","kind":"add"}],"status":"failed"}}
{"type":"item.started","item":{"id":"item_1","type":"mcp_tool_call","server":"docs","tool":"search","arguments":{"path":"/workspace/docs","query":"rounding"},"status":"in_progress"}}
{"type":"item.updated","item":{"id":"item_1","type":"mcp_tool_call","server":"docs","tool":"search","arguments":{"path":"/workspace/docs","query":"rounding"},"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"mcp_tool_call","server":"docs","tool":"search","arguments":{"path":"/workspace/docs","query":"rounding"},"result":null,"error":{"message":"server gone"},"status":"failed"}}
{"type":"item.completed","item":{"id":"item_2","type":"error","message":"model refused"}}`,
			counts: map[schemas.EventType]int{schemas.EventModelResponse: 2, schemas.EventToolCall: 2,
				schemas.EventToolResult: 2, schemas.EventError: 1},
			check: func(t *testing.T, res *schemas.ParseResult) {
				calls := eventsOf(res.Events, schemas.EventToolCall)
				results := eventsOf(res.Events, schemas.EventToolResult)
				if calls[0].ToolName != "apply_patch" || calls[0].Target != "/workspace/a.py, /workspace/b.py" || !results[0].IsError {
					t.Errorf("patch = %+v / %+v", calls[0], results[0])
				}
				if calls[1].ToolName != "docs.search" || calls[1].Target != "/workspace/docs" || !results[1].IsError {
					t.Errorf("mcp = %+v / %+v", calls[1], results[1])
				}
				if res.Claim == nil || res.Claim.Made || res.Claim.Text != "" {
					t.Errorf("claim = %+v, want an observed absence", res.Claim)
				}
			},
		},
		{
			name: "a later message replaces the final text",
			raw: `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"All tests pass."}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Actually the build is still broken."}}`,
			counts: map[schemas.EventType]int{schemas.EventModelResponse: 1},
			check: func(t *testing.T, res *schemas.ParseResult) {
				if res.Claim == nil || res.Claim.Made || res.Claim.Text != "Actually the build is still broken." {
					t.Errorf("claim = %+v", res.Claim)
				}
			},
		},
		{
			name:   "noise is skipped and gives no claim",
			raw:    "Reading additional input from stdin...\nnot json {\n{\"type\":\"session.mystery\"}\n{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"no id\"}}\n{broken",
			counts: map[schemas.EventType]int{},
			check: func(t *testing.T, res *schemas.ParseResult) {
				if res.Claim != nil {
					t.Errorf("claim = %+v, want nil", res.Claim)
				}
			},
		},
		{
			name:   "a run with no message is an observed non-claim",
			raw:    `{"type":"thread.started","thread_id":"x"}` + "\n" + `{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized"}}`,
			counts: map[schemas.EventType]int{schemas.EventError: 1},
			check: func(t *testing.T, res *schemas.ParseResult) {
				if res.Claim == nil || res.Claim.Made {
					t.Errorf("claim = %+v", res.Claim)
				}
				if s := res.Events[0].Summary; s != "turn failed: unexpected status 401 Unauthorized" {
					t.Errorf("summary = %q", s)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := parseCodex(t, tc.raw)
			got := countEvents(res.Events)
			if len(got) != len(tc.counts) {
				t.Errorf("events = %v, want %v", got, tc.counts)
			}
			for typ, n := range tc.counts {
				if got[typ] != n {
					t.Errorf("%s = %d, want %d", typ, got[typ], n)
				}
			}
			tc.check(t, res)
		})
	}
}

func TestCodexCommandTarget(t *testing.T) {
	cases := map[string]string{
		`/bin/sh -c 'python -m pytest -q'`:                         "python -m pytest -q",
		`/bin/sh -lc 'python -m pytest -q'`:                        "python -m pytest -q",
		`/bin/bash -lc ls`:                                         "ls",
		`/bin/sh -c "grep -n 'def add' money.py && echo \"it's\""`: `grep -n 'def add' money.py && echo "it's"`,
		`/bin/sh -c "echo \$HOME \\ done"`:                         `echo $HOME \ done`,
		"/bin/sh -c 'cat <<EOF\nx\nEOF'":                           "cat <<EOF\nx\nEOF",
		`/bin/sh -c 'a' 'b'`:                                       `/bin/sh -c 'a' 'b'`,
		`/bin/sh -c "a" "b"`:                                       `/bin/sh -c "a" "b"`,
		`python -m pytest`:                                         "python -m pytest",
		`  /usr/bin/zsh -c 'make test'  `:                          "make test",
	}
	for in, want := range cases {
		if got := codexCommandTarget(in); got != want {
			t.Errorf("codexCommandTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCodexCollabSpawn: a collab_tool_call spawn_agent item is a tool call
// that names the sub-thread it started. The item shape follows the field
// names in the Codex 0.169.1 binary, not a recorded stream.
func TestCodexCollabSpawn(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t0"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"item_1","type":"collab_tool_call","tool":"spawn_agent","sender_thread_id":"t0","receiver_thread_ids":[],"prompt":"check the tests","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"collab_tool_call","tool":"spawn_agent","sender_thread_id":"t0","receiver_thread_ids":["t1"],"prompt":"check the tests","status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"done"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
	}, "\n")
	res, err := newCodexParser().Parse(context.Background(), &schemas.RunContext{Run: &schemas.Run{RunID: "r_x"}}, []byte(stream))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range res.Events {
		if e.Type == schemas.EventToolCall && e.ToolCallID == "item_1" {
			found = true
			if e.ToolName != "spawn_agent" || e.AgentTarget != CodexThreadPrefix+"t1" || e.AgentName != "check the tests" {
				t.Errorf("spawn call = %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("no spawn call in %+v", res.Events)
	}
}
