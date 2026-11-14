package bifrost

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The fixtures in testdata/openai/ are detail rows captured by
// TestIntegrationOpenAI (maximhq/bifrost:v2.5.1, built-in openai provider,
// bench fault proxy in scripted mode, one virtual key on oa-slot-1) and
// ledger.json is that run's proxy ledger. Bifrost v2.5.1 sends chat
// completions for this model upstream as POST /v1/responses, so every
// conversation row holds a Responses object or event stream.
var openAIFixtures = []string{
	"chat-json.json", "chat-stream-tools.json", "chat-tool-result.json", "responses-json.json",
	"responses-stream-tools.json", "responses-tool-result.json", "responses-stream.json",
	"count-tokens.json", "list-models.json", "retry-ok.json", "retry-fail.json",
}

func openAIRows(t *testing.T) map[string]*parsedRow {
	t.Helper()
	out := map[string]*parsedRow{}
	for _, f := range openAIFixtures {
		p, err := parseDetail(readFixture(t, filepath.Join("openai", f)))
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		out[f] = p
	}
	return out
}

func openAILedger(t *testing.T) []schemas.LedgerEntry {
	t.Helper()
	var l []schemas.LedgerEntry
	if err := json.Unmarshal(readFixture(t, filepath.Join("openai", "ledger.json")), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestParseDetailOpenAIFixtures(t *testing.T) {
	rows := openAIRows(t)
	tests := []struct {
		file             string
		kind, status     string
		stream           bool
		attempts         int
		tools            []string // name:argsHash
		results          []string
		errStatus        int
		nonce            bool
		wantResponseID   bool
		wantTokens, cost bool
	}{
		{file: "chat-json.json", kind: "message", status: "success", attempts: 1, wantResponseID: true, wantTokens: true, cost: true},
		{file: "chat-stream-tools.json", kind: "message", status: "success", stream: true, attempts: 1,
			tools: []string{"Read:" + mustHash(t, map[string]any{"file_path": ""})}, wantResponseID: true, wantTokens: true, cost: true},
		{file: "chat-tool-result.json", kind: "message", status: "success", attempts: 1, results: []string{"call_fixture0001"},
			wantResponseID: true, wantTokens: true, cost: true},
		{file: "responses-json.json", kind: "message", status: "success", attempts: 1, wantResponseID: true, wantTokens: true, cost: true},
		{file: "responses-stream-tools.json", kind: "message", status: "success", stream: true, attempts: 1,
			tools: []string{"shell:" + mustHash(t, map[string]any{"command": ""})}, wantResponseID: true, wantTokens: true, cost: true},
		{file: "responses-tool-result.json", kind: "message", status: "success", attempts: 1, results: []string{"call_fixture0002"},
			wantResponseID: true, wantTokens: true, cost: true},
		{file: "responses-stream.json", kind: "message", status: "success", stream: true, attempts: 1, wantResponseID: true, wantTokens: true, cost: true},
		{file: "count-tokens.json", kind: "count_tokens", status: "success", attempts: 1},
		{file: "list-models.json", kind: "models", status: "success"},
		{file: "retry-ok.json", kind: "message", status: "success", attempts: 2, wantResponseID: true, wantTokens: true, cost: true},
		{file: "retry-fail.json", kind: "message", status: "error", attempts: 2, errStatus: 503, nonce: true},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			p := rows[tt.file]
			r := p.Req
			if r.Kind != tt.kind || r.Status != tt.status || r.Stream != tt.stream || r.Provider != "openai" || len(r.Attempts) != tt.attempts {
				t.Errorf("kind %s status %s stream %t provider %s attempts %d", r.Kind, r.Status, r.Stream, r.Provider, len(r.Attempts))
			}
			if tt.wantResponseID != strings.HasPrefix(r.ResponseID, "resp_") {
				t.Errorf("response id %q", r.ResponseID)
			}
			if tt.kind == "message" && (r.ModelRequested != testOpenAIModel || r.ModelServed != testOpenAIModel) {
				t.Errorf("models %q -> %q", r.ModelRequested, r.ModelServed)
			}
			if tt.wantTokens != (r.Tokens != nil) || (r.Tokens != nil && (r.Tokens.CacheRead == 0 || r.Tokens.CacheWrite != 0 || r.Tokens.Out != 32)) {
				t.Errorf("tokens %+v", r.Tokens)
			}
			if tt.cost != (r.BifrostUSD != nil && *r.BifrostUSD > 0) {
				t.Errorf("cost %v", r.BifrostUSD)
			}
			var tools []string
			for _, tc := range r.ToolCalls {
				if !strings.HasPrefix(tc.ID, "call_") {
					t.Errorf("tool call id %q", tc.ID)
				}
				tools = append(tools, tc.Name+":"+tc.ArgsHash)
			}
			if strings.Join(tools, ",") != strings.Join(tt.tools, ",") || strings.Join(r.ToolResultIDs, ",") != strings.Join(tt.results, ",") {
				t.Errorf("tools %v results %v", tools, r.ToolResultIDs)
			}
			if r.ErrorStatus != tt.errStatus || (p.Nonce != "") != tt.nonce || (tt.nonce && p.NonceCall != 2) {
				t.Errorf("error %d %q nonce %q call %d", r.ErrorStatus, r.ErrorMessage, p.Nonce, p.NonceCall)
			}
			if (tt.kind != "models") != (p.ReqSHA256 != "") {
				t.Errorf("request hash %q", p.ReqSHA256)
			}
		})
	}
}

func TestJoinLedgerOpenAIFixtures(t *testing.T) {
	rows := openAIRows(t)
	ledger := openAILedger(t)
	var list []*parsedRow
	for _, f := range openAIFixtures {
		list = append(list, rows[f])
	}
	j := joinLedger(list, ledger, "oa-slot-1")
	if !j.complete() {
		t.Fatalf("join incomplete: %s", j.reason())
	}
	if len(j.Entries) != 12 {
		t.Errorf("joinable entries = %d, want 12 (11 message, 1 count_tokens)", len(j.Entries))
	}
	want := map[string]string{"retry-ok.json": "inject pass", "retry-fail.json": "inject inject", "count-tokens.json": "pass", "list-models.json": ""}
	for f, w := range want {
		var got []string
		for _, i := range j.Rows[rows[f].Req.ID] {
			got = append(got, j.Entries[i].Action)
		}
		if strings.Join(got, " ") != w {
			t.Errorf("%s covers %v, want %q", f, got, w)
		}
	}
	// Every passed conversation call: the row's response id and token
	// buckets equal what the proxy recorded for it.
	for _, f := range openAIFixtures {
		r := rows[f].Req
		idx := j.Rows[r.ID]
		if r.Kind != "message" || r.Status != "success" || len(idx) == 0 {
			continue
		}
		e := j.Entries[idx[len(idx)-1]]
		if e.ResponseID != r.ResponseID || e.Tokens == nil || r.Tokens == nil || *e.Tokens != *r.Tokens {
			t.Errorf("%s: ledger %s %+v, row %s %+v", f, e.ResponseID, e.Tokens, r.ResponseID, r.Tokens)
		}
	}
}

func TestOpenAIToolCalls(t *testing.T) {
	readHash := mustHash(t, map[string]any{"file_path": "a.go"})
	cases := []struct {
		name   string
		raw    string
		stream bool
		ok     bool
		want   []string // id:name:argsHash
	}{
		{"chat json", `{"object":"chat.completion","choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"a.go\"}"}}]}}]}`,
			false, true, []string{"call_1:Read:" + readHash}},
		{"chat stream deltas", `data: {"object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_2","function":{"name":"Read","arguments":""}}]}}]}` + "\n\n" +
			`data: {"object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":"}}]}}]}` + "\n\n" +
			`data: {"object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}` + "\n\n" +
			"data: [DONE]", true, true, []string{"call_2:Read:" + readHash}},
		{"responses custom tool", `{"object":"response","output":[{"type":"reasoning","id":"rs_1"},{"type":"custom_tool_call","id":"ctc_1","call_id":"call_3","name":"apply_patch","input":"*** Begin Patch"}]}`,
			false, true, []string{"call_3:apply_patch:" + mustHash(t, "*** Begin Patch")}},
		{"responses completed only", `{"type":"response.created","response":{"id":"resp_1","output":[]}}` + "\n\n" +
			`{"type":"response.completed","response":{"id":"resp_1","output":[{"type":"function_call","call_id":"call_4","name":"Read","arguments":"{\"file_path\":\"a.go\"}"}]}}`,
			true, true, []string{"call_4:Read:" + readHash}},
		{"responses text", `{"object":"response","output":[{"type":"message","content":[]}]}`, false, true, nil},
		{"anthropic message", `{"type":"message","content":[]}`, false, false, nil},
		{"anthropic stream", `{"type":"message_start","message":{"id":"msg_1"}}`, true, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, ok := openAIToolCalls(tc.raw, tc.stream)
			var got []string
			for _, c := range calls {
				got = append(got, c.ID+":"+c.Name+":"+c.ArgsHash)
			}
			if ok != tc.ok || strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("ok %t calls %v, want %t %v", ok, got, tc.ok, tc.want)
			}
		})
	}
}

func TestOpenAIToolResultIDs(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`{"messages":[{"role":"assistant","tool_calls":[{"id":"call_a"}]},{"role":"tool","tool_call_id":"call_a","content":"x"},{"role":"tool","tool_call_id":"call_b"}]}`, "call_a,call_b"},
		{`{"input":[{"type":"function_call","call_id":"call_c"},{"type":"function_call_output","call_id":"call_c"},{"type":"custom_tool_call_output","call_id":"call_d"}]}`, "call_c,call_d"},
		{`{"input":"just text"}`, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(toolResultIDs(tc.raw), ","); got != tc.want {
			t.Errorf("toolResultIDs(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
