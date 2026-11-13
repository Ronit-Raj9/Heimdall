package faultproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// anthropicMessage is the Messages API response shape, decoded strictly
// enough to catch a malformed scripted reply.
type anthropicMessage struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Role         string           `json:"role"`
	Model        string           `json:"model"`
	Content      []map[string]any `json:"content"`
	StopReason   string           `json:"stop_reason"`
	StopSequence *string          `json:"stop_sequence"`
	Usage        struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

const toolsBody = `{"model":"claude-opus-x","max_tokens":64,"messages":[{"role":"user","content":"fix it"}],` +
	`"tools":[{"name":"Bash","input_schema":{"type":"object","required":["command","timeout"],"properties":{"command":{"type":"string"},"timeout":{"type":"integer"}}}},{"name":"Read"}]}`

func TestScriptedNonStreaming(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeNone, RunID: "r1"})
	cases := []struct {
		name      string
		body      string
		wantStop  string
		wantBlock string
		wantModel string
	}{
		{"tools: first turn is tool_use", toolsBody, "tool_use", "tool_use", "claude-opus-x"},
		{"tools: later turns are text", toolsBody, "end_turn", "text", "claude-opus-x"},
		{"no tools", msgBody, "end_turn", "text", "claude-sonnet-4-6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", tc.body)
			if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			var m anthropicMessage
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&m); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
			if !strings.HasPrefix(m.ID, "msg_") || m.Type != "message" || m.Role != "assistant" || m.Model != tc.wantModel {
				t.Errorf("header fields = %+v", m)
			}
			if m.StopReason != tc.wantStop || len(m.Content) != 1 || m.Content[0]["type"] != tc.wantBlock {
				t.Fatalf("stop %q content %v", m.StopReason, m.Content)
			}
			if tc.wantBlock == "tool_use" {
				b := m.Content[0]
				wantInput := map[string]any{"command": "", "timeout": float64(0)}
				if b["name"] != "Bash" || !strings.HasPrefix(b["id"].(string), "toolu_") || !reflect.DeepEqual(b["input"], wantInput) {
					t.Errorf("tool_use block = %v", b)
				}
			}
			if m.Usage.Input != int64(len(tc.body)/4) || m.Usage.Output != 32 || m.Usage.CacheRead != 0 || m.Usage.CacheWrite != 0 {
				t.Errorf("usage = %+v", m.Usage)
			}
			entries := env.entries(t)
			last := entries[len(entries)-1]
			want := schemas.Tokens{Fresh: int64(len(tc.body) / 4), Out: 32}
			if last.ResponseID != m.ID || last.Tokens == nil || *last.Tokens != want {
				t.Errorf("ledger id %q tokens %+v", last.ResponseID, last.Tokens)
			}
		})
	}
}

func TestScriptedStreaming(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantStop string
	}{
		{"text", `{"model":"m1","stream":true,"messages":[]}`, "end_turn"},
		{"tool_use", strings.Replace(toolsBody, `{"model"`, `{"stream":true,"model"`, 1), "tool_use"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, UpstreamScripted, "")
			resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", tc.body)
			if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			events := parseSSE(t, body)
			var types []string
			for _, ev := range events {
				types = append(types, ev.name)
			}
			want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
			if !reflect.DeepEqual(types, want) {
				t.Fatalf("event types %v, want %v", types, want)
			}
			var start struct {
				Message anthropicMessage `json:"message"`
			}
			mustDecode(t, events[0].data, &start)
			if !strings.HasPrefix(start.Message.ID, "msg_") || start.Message.Role != "assistant" || start.Message.Usage.Input != int64(len(tc.body)/4) {
				t.Errorf("message_start = %+v", start.Message)
			}
			var delta struct {
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			mustDecode(t, events[2].data, &delta)
			if tc.wantStop == "tool_use" {
				var input map[string]any
				if delta.Delta.Type != "input_json_delta" || json.Unmarshal([]byte(delta.Delta.PartialJSON), &input) != nil {
					t.Errorf("tool delta = %+v", delta.Delta)
				}
			} else if delta.Delta.Type != "text_delta" || delta.Delta.Text == "" {
				t.Errorf("text delta = %+v", delta.Delta)
			}
			var md struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					Output int64 `json:"output_tokens"`
				} `json:"usage"`
			}
			mustDecode(t, events[4].data, &md)
			if md.Delta.StopReason != tc.wantStop || md.Usage.Output != 32 {
				t.Errorf("message_delta = %+v", md)
			}
			entries := env.entries(t)
			last := entries[len(entries)-1]
			if last.ResponseID != start.Message.ID || !last.Stream || last.Tokens == nil || last.Tokens.Out != 32 {
				t.Errorf("ledger entry = %+v", last)
			}
		})
	}
}

func TestScriptedAuxiliaryRoutes(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	cases := []struct {
		name, method, path string
		status             int
		check              func(t *testing.T, body []byte)
	}{
		{"count_tokens", http.MethodPost, "/v1/messages/count_tokens", 200, func(t *testing.T, body []byte) {
			var v map[string]int64
			mustDecode(t, body, &v)
			if len(v) != 1 || v["input_tokens"] != int64(len(msgBody)/4) {
				t.Errorf("count_tokens = %v", v)
			}
		}},
		{"models list", http.MethodGet, "/v1/models", 200, func(t *testing.T, body []byte) {
			var v struct {
				Data []struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
				HasMore bool   `json:"has_more"`
				FirstID string `json:"first_id"`
			}
			mustDecode(t, body, &v)
			if len(v.Data) == 0 || v.Data[0].Type != "model" || v.FirstID != v.Data[0].ID || v.HasMore {
				t.Errorf("models = %+v", v)
			}
		}},
		{"one model", http.MethodGet, "/v1/models/claude-x", 200, func(t *testing.T, body []byte) {
			var v map[string]any
			mustDecode(t, body, &v)
			if v["id"] != "claude-x" || v["type"] != "model" {
				t.Errorf("model = %v", v)
			}
		}},
		{"other is 404", http.MethodPost, "/v1/complete", 404, func(t *testing.T, body []byte) {
			if decodeError(t, body).Error.Type != "not_found_error" {
				t.Errorf("body %s", body)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := env.call(t, tc.method, tc.path, "auth-a", msgBody)
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			tc.check(t, body)
		})
	}
}

func TestScriptedToolInput(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   map[string]any
	}{
		{"empty", ``, map[string]any{}},
		{"invalid", `{`, map[string]any{}},
		{"no required", `{"type":"object","properties":{"a":{"type":"string"}}}`, map[string]any{}},
		{"types", `{"required":["s","n","b","a","o","u","multi"],"properties":{"s":{"type":"string"},"n":{"type":"number"},"b":{"type":"boolean"},"a":{"type":"array"},"o":{"type":"object"},"multi":{"type":["integer","null"]}}}`,
			map[string]any{"s": "", "n": 0, "b": false, "a": []any{}, "o": map[string]any{}, "u": "", "multi": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scriptedToolInput(json.RawMessage(tc.schema)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

type sseFrame struct {
	name string
	data []byte
}

// parseSSE splits an SSE body into frames and checks that every event line
// matches its payload's type.
func parseSSE(t *testing.T, body []byte) []sseFrame {
	t.Helper()
	var out []sseFrame
	var cur sseFrame
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = []byte(strings.TrimPrefix(line, "data: "))
		case line == "":
			var typ struct {
				Type string `json:"type"`
			}
			mustDecode(t, cur.data, &typ)
			if typ.Type != cur.name {
				t.Errorf("event %q carries type %q", cur.name, typ.Type)
			}
			out = append(out, cur)
			cur = sseFrame{}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}
