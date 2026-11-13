package bifrost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The fixtures in testdata/ are detail rows captured from the spike stack
// (maximhq/bifrost:v2.5.1, spike fault proxy in scripted mode, one virtual key
// on slot-b) and ledger.json is that slot's proxy ledger in schemas.LedgerEntry
// form. same-body-pass and retry-ok carry the same request body on purpose.
func TestParseDetailFixtures(t *testing.T) {
	note := mustHash(t, map[string]any{"note": "scripted tool call"})
	tests := []struct {
		file       string
		kind       string
		status     string
		stream     bool
		respID     string
		tokens     *schemas.Tokens
		attempts   int
		tools      []string // name:argsHash
		results    []string
		errStatus  int
		nonce      string
		upstream   bool
		wantSHA256 bool
	}{
		{file: "json.json", kind: "message", status: "success", respID: "msg_XHvl8y2GgyHAOtqDhPuc8HoP",
			tokens: &schemas.Tokens{Fresh: 16, CacheRead: 16, CacheWrite: 32, Out: 10}, attempts: 1, upstream: true, wantSHA256: true},
		{file: "stream-tools.json", kind: "message", status: "success", stream: true, respID: "msg_3DKXf4zB0T7kvHbEVdJM0SrR",
			tokens: &schemas.Tokens{Fresh: 18, CacheRead: 32, CacheWrite: 32, Out: 10}, attempts: 1, tools: []string{"Read:" + note}, upstream: true, wantSHA256: true},
		{file: "tools-json.json", kind: "message", status: "success", respID: "msg_lyYshndOGNL7OOMHfZSfdihf",
			tokens: &schemas.Tokens{Fresh: 18, CacheRead: 48, CacheWrite: 32, Out: 10}, attempts: 1, tools: []string{"Read:" + note}, upstream: true, wantSHA256: true},
		{file: "tool-result.json", kind: "message", status: "success", respID: "msg_BbRtS0SdwrY03uYuFxSbGYsI", attempts: 1,
			tokens:  &schemas.Tokens{Fresh: 87, CacheRead: 64, CacheWrite: 32, Out: 10},
			results: []string{"toolu_fixture000000000001"}, upstream: true, wantSHA256: true},
		{file: "count-tokens.json", kind: "count_tokens", status: "success", attempts: 1, upstream: true, wantSHA256: true},
		{file: "retry-ok.json", kind: "message", status: "success", respID: "msg_fl7F3iP4kqDFAO6BZWqViwDp",
			tokens: &schemas.Tokens{Fresh: 17, CacheRead: 32, CacheWrite: 32, Out: 10}, attempts: 2, upstream: true, wantSHA256: true},
		{file: "retry-fail.json", kind: "message", status: "error", attempts: 2, errStatus: 503, nonce: "l87QjSk5s6tiSBGU", upstream: true, wantSHA256: true},
		{file: "list-models.json", kind: "models", status: "success"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			raw := readFixture(t, tt.file)
			p, err := parseDetail(raw)
			if err != nil {
				t.Fatalf("parseDetail: %v", err)
			}
			r := p.Req
			if r.Kind != tt.kind || r.Status != tt.status || r.Stream != tt.stream || r.ResponseID != tt.respID {
				t.Errorf("kind %s status %s stream %t resp %q", r.Kind, r.Status, r.Stream, r.ResponseID)
			}
			if r.Provider != "anthropic" || r.LatencyMS <= 0 || r.Timestamp.IsZero() || r.ParentID != "" {
				t.Errorf("provider %q latency %v ts %v parent %q", r.Provider, r.LatencyMS, r.Timestamp, r.ParentID)
			}
			if tt.kind == "message" && (r.ModelRequested != testModel || r.ModelServed != testModel) {
				t.Errorf("models %q -> %q", r.ModelRequested, r.ModelServed)
			}
			if (r.Tokens == nil) != (tt.tokens == nil) || (r.Tokens != nil && *r.Tokens != *tt.tokens) {
				t.Errorf("tokens = %+v, want %+v", r.Tokens, tt.tokens)
			}
			if tt.tokens != nil && (r.BifrostUSD == nil || *r.BifrostUSD <= 0) {
				t.Errorf("cost = %v", r.BifrostUSD)
			}
			if len(r.Attempts) != tt.attempts {
				t.Errorf("attempts = %+v", r.Attempts)
			}
			for i, a := range r.Attempts {
				if a.N != i+1 || a.KeyName != "slot-b" {
					t.Errorf("attempt %d = %+v", i, a)
				}
			}
			var tools []string
			for _, tc := range r.ToolCalls {
				if !strings.HasPrefix(tc.ID, "toolu_") {
					t.Errorf("tool call id %q", tc.ID)
				}
				tools = append(tools, tc.Name+":"+tc.ArgsHash)
			}
			if strings.Join(tools, ",") != strings.Join(tt.tools, ",") {
				t.Errorf("tool calls = %v, want %v", tools, tt.tools)
			}
			if strings.Join(r.ToolResultIDs, ",") != strings.Join(tt.results, ",") {
				t.Errorf("tool results = %v", r.ToolResultIDs)
			}
			if r.ErrorStatus != tt.errStatus || p.Nonce != tt.nonce || p.Upstream != tt.upstream || (p.ReqSHA256 != "") != tt.wantSHA256 {
				t.Errorf("error %d nonce %q upstream %t sha %q", r.ErrorStatus, p.Nonce, p.Upstream, p.ReqSHA256)
			}
			if !strings.Contains(string(r.Raw), `"raw_request"`) || !json.Valid(r.Raw) {
				t.Errorf("raw row lost raw_request or is invalid")
			}
		})
	}
}

func TestParseDetailErrors(t *testing.T) {
	tests := []struct {
		name, in string
	}{
		{"not json", "{"},
		{"no id", `{"object":"responses"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseDetail([]byte(tt.in)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestParseDetailRetryFailMessage(t *testing.T) {
	p, err := parseDetail(readFixture(t, "retry-fail.json"))
	if err != nil {
		t.Fatalf("parseDetail: %v", err)
	}
	a := p.Req.Attempts
	if a[0].StatusCode != 503 || a[0].FailReason != "api_error" || a[0].FailureClass != "transient" || a[1].StatusCode != 503 {
		t.Errorf("attempts = %+v", a)
	}
	if !strings.Contains(p.Req.ErrorMessage, "faultproxy injected 503") || p.Req.ModelRequested != testModel {
		t.Errorf("error message %q model %q", p.Req.ErrorMessage, p.Req.ModelRequested)
	}
}

func TestToolCallsFromRaw(t *testing.T) {
	stream := func(events ...string) string { return strings.Join(events, "\n\n") }
	ab := mustHash(t, map[string]any{"a": 1, "b": []any{true, "x"}})
	tests := []struct {
		name   string
		raw    string
		stream bool
		want   []schemas.ToolCallRef
	}{
		{
			name: "json body with file_path target",
			raw:  `{"type":"message","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/w/a.py","limit":10}}]}`,
			want: []schemas.ToolCallRef{{ID: "toolu_1", Name: "Read", ArgsHash: mustHash(t, map[string]any{"file_path": "/w/a.py", "limit": 10}), Target: "/w/a.py"}},
		},
		{
			name: "key order does not change the hash",
			raw:  `{"content":[{"type":"tool_use","id":"t","name":"X","input":{"b":[true,"x"],"a":1}}]}`,
			want: []schemas.ToolCallRef{{ID: "t", Name: "X", ArgsHash: ab}},
		},
		{
			name: "empty input hashes as an empty object",
			raw:  `{"content":[{"type":"tool_use","id":"t","name":"Ls"}]}`,
			want: []schemas.ToolCallRef{{ID: "t", Name: "Ls", ArgsHash: mustHash(t, map[string]any{})}},
		},
		{
			name: "stream with split input and data framing", stream: true,
			raw: stream(`event: message_start`+"\n"+`data: {"type":"message_start","message":{"id":"msg_1"}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_a","name":"Bash","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"comm"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"and\":\"pytest -q\"}"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_b","name":"Grep","input":{}}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"pattern\":\"TODO\",\"path\":\"src\"}"}}`,
				`{"type":"content_block_stop","index":1}`),
			want: []schemas.ToolCallRef{
				{ID: "toolu_a", Name: "Bash", ArgsHash: mustHash(t, map[string]any{"command": "pytest -q"}), Target: "pytest -q"},
				{ID: "toolu_b", Name: "Grep", ArgsHash: mustHash(t, map[string]any{"pattern": "TODO", "path": "src"}), Target: "src"},
			},
		},
		{
			name: "cut stream leaves the hash empty", stream: true,
			raw: stream(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_c","name":"Edit","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_pa"}}`),
			want: []schemas.ToolCallRef{{ID: "toolu_c", Name: "Edit"}},
		},
		{name: "error envelope has no calls", raw: `{"type":"error","error":{"message":"x"}}`},
		{name: "empty", raw: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toolCallsFromRaw(tt.raw, tt.stream)
			if err != nil {
				t.Fatalf("toolCallsFromRaw: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("call %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestToolResultIDs(t *testing.T) {
	tests := []struct {
		name, raw, want string
	}{
		{"string content", `{"messages":[{"role":"user","content":"hi"}]}`, ""},
		{"blocks with repeat", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"}]},{"role":"user","content":[{"type":"text","text":"x"},{"type":"tool_result","tool_use_id":"b"},{"type":"tool_result","tool_use_id":"a"}]}]}`, "a,b"},
		{"not json", `{`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Join(toolResultIDs(tt.raw), ","); got != tt.want {
				t.Errorf("toolResultIDs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindNonce(t *testing.T) {
	tests := []struct {
		name  string
		texts []string
		nonce string
		call  int
	}{
		{"bench proxy", []string{"Overloaded: the model is temporarily unavailable. ref nonce_ab12cd call 7"}, "nonce_ab12cd", 7},
		{"bench proxy in raw body", []string{"", `{"error":{"message":"Internal server error. ref nonce_x call 2"}}`}, "nonce_x", 2},
		{"spike proxy", []string{"faultproxy injected 503 on call 1 (nonce AiyfKtNuAWIeUb9M)"}, "AiyfKtNuAWIeUb9M", 0},
		{"none", []string{"rate limited", ""}, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, c := findNonce(tt.texts...)
			if n != tt.nonce || c != tt.call {
				t.Errorf("findNonce = %q %d", n, c)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want *schemas.Tokens
	}{
		{"absent", `null`, nil},
		{"no prompt tokens", `{"completion_tokens":3}`, nil},
		{"no cache", `{"prompt_tokens":12,"completion_tokens":3}`, &schemas.Tokens{Fresh: 12, Out: 3}},
		{"alternate field names", `{"prompt_tokens":10,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":2}}`, &schemas.Tokens{Fresh: 4, CacheRead: 4, CacheWrite: 2, Out: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var u *tokenUsage
			if err := json.Unmarshal([]byte(tt.in), &u); err != nil {
				t.Fatal(err)
			}
			got := u.tokens()
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("tokens = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func mustHash(t *testing.T, v any) string {
	t.Helper()
	h, err := utils.HashJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
