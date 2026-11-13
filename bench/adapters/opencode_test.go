package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func parseOpenCode(t *testing.T, raw []byte) *schemas.ParseResult {
	t.Helper()
	rc, _ := fixture(t, commandAgent())
	rc.Agent.Gateway.Provider = "anthropic"
	res, err := newOpenCodeParser().Parse(context.Background(), rc, raw)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestOpenCodeParseRealOutput reads output captured from opencode-ai 1.19.5
// (`opencode run --format json`) in the bench image, against a fake
// OpenAI-compatible endpoint that asked for one bash call and then answered.
func TestOpenCodeParseRealOutput(t *testing.T) {
	res := parseOpenCode(t, readTestdata(t, "opencode-1.19.5-fakeapi.jsonl"))
	var types []string
	for _, ev := range res.Events {
		types = append(types, string(ev.Type))
		if ev.Source != schemas.SourceAgentStream || ev.Evidence != schemas.EvidenceNative || ev.Provider != "anthropic" {
			t.Errorf("event %s: source %s evidence %s provider %q", ev.EventID, ev.Source, ev.Evidence, ev.Provider)
		}
	}
	want := "model_response tool_call tool_result model_response"
	if got := strings.Join(types, " "); got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	step1, call, result, step2 := res.Events[0], res.Events[1], res.Events[2], res.Events[3]
	if step1.TurnIndex != 1 || step2.TurnIndex != 2 || call.TurnIndex != 1 {
		t.Errorf("turns = %d %d %d", step1.TurnIndex, call.TurnIndex, step2.TurnIndex)
	}
	if step1.Tokens == nil || *step1.Tokens != (schemas.Tokens{Fresh: 100, Out: 10}) {
		t.Errorf("step 1 tokens = %+v", step1.Tokens)
	}
	if call.ToolName != "bash" || call.ToolCallID != "call_fake1" || call.ParentEventID != step1.EventID ||
		call.Target != "echo bench-fake > fake.txt && cat fake.txt" || call.ArgsHash == "" {
		t.Errorf("tool call = %+v", call)
	}
	if result.ParentEventID != call.EventID || result.IsError || result.Summary != "bench-fake" || result.ResultHash == "" {
		t.Errorf("tool result = %+v", result)
	}
	if step1.TS.IsZero() || step1.TS.Year() != 2026 {
		t.Errorf("timestamp = %v", step1.TS)
	}
	if res.Claim == nil || !res.Claim.Made || res.Claim.Source != schemas.EvidenceNative || res.Claim.Matched != "all tests pass" {
		t.Errorf("claim = %+v", res.Claim)
	}
}

// TestOpenCodeParseAPIError reads OpenCode's output when the gateway refuses
// the first call (captured from 1.19.5 against a fake 400).
func TestOpenCodeParseAPIError(t *testing.T) {
	res := parseOpenCode(t, readTestdata(t, "opencode-1.19.5-apierror.jsonl"))
	if len(res.Events) != 1 || res.Events[0].Type != schemas.EventError || !res.Events[0].IsError {
		t.Fatalf("events = %+v", res.Events)
	}
	if s := res.Events[0].Summary; s != "api error APIError: fake upstream failure (400)" {
		t.Errorf("summary = %q", s)
	}
	if res.Claim == nil || res.Claim.Made {
		t.Errorf("claim = %+v, want an observed non-claim", res.Claim)
	}
}

func TestOpenCodeParseEdges(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		check func(t *testing.T, res *schemas.ParseResult)
	}{
		{"empty output has no claim", "", func(t *testing.T, res *schemas.ParseResult) {
			if len(res.Events) != 0 || res.Claim != nil {
				t.Errorf("res = %+v", res)
			}
		}},
		{"noise and unknown lines are skipped", "starting...\n{\"type\":\"session_meta\"}\n{not json\n",
			func(t *testing.T, res *schemas.ParseResult) {
				if len(res.Events) != 0 || res.Claim != nil {
					t.Errorf("res = %+v", res)
				}
			}},
		{"failed tool and file target", `{"type":"step_start","timestamp":1794613577269,"part":{"messageID":"m1"}}
{"type":"tool_use","part":{"tool":"read","callID":"c1","state":{"status":"error","input":{"filePath":"/workspace/a.py"},"error":"File not found"}}}
{"type":"step_finish","part":{"tokens":{"input":5,"output":7,"reasoning":3,"cache":{"read":40,"write":2}}}}`,
			func(t *testing.T, res *schemas.ParseResult) {
				if len(res.Events) != 3 {
					t.Fatalf("events = %+v", res.Events)
				}
				call, result := res.Events[1], res.Events[2]
				if call.Target != "/workspace/a.py" || !result.IsError || result.Summary != "File not found" {
					t.Errorf("call %+v result %+v", call, result)
				}
				if tk := res.Events[0].Tokens; tk == nil || *tk != (schemas.Tokens{Fresh: 5, CacheRead: 40, CacheWrite: 2, Out: 10}) {
					t.Errorf("tokens = %+v", tk)
				}
				if res.Claim == nil || res.Claim.Made {
					t.Errorf("claim = %+v", res.Claim)
				}
			}},
		{"claim reads the last message only", `{"type":"step_start","part":{"messageID":"m1"}}
{"type":"text","part":{"messageID":"m1","text":"Fixed the bug."}}
{"type":"step_start","part":{"messageID":"m2"}}
{"type":"text","part":{"messageID":"m2","text":"I could not make the tests pass."}}`,
			func(t *testing.T, res *schemas.ParseResult) {
				if res.Claim == nil || res.Claim.Made || !strings.Contains(res.Claim.Text, "could not") {
					t.Errorf("claim = %+v", res.Claim)
				}
				if res.Events[0].Summary != "Fixed the bug." {
					t.Errorf("summary = %q", res.Events[0].Summary)
				}
			}},
		{"string error", `{"type":"error","error":"session aborted"}`, func(t *testing.T, res *schemas.ParseResult) {
			if len(res.Events) != 1 || res.Events[0].Summary != "api error session aborted" {
				t.Errorf("events = %+v", res.Events)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, parseOpenCode(t, []byte(tt.raw)))
		})
	}
}

func TestOpenCodeParserPart(t *testing.T) {
	p := newOpenCodeParser()
	if p.GetName() != ParserOpenCode || p.GetVersion() == "" {
		t.Errorf("part = %s %s", p.GetName(), p.GetVersion())
	}
}
