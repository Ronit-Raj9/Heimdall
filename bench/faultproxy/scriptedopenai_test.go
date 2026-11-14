package faultproxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	chatToolsBody = `{"model":"gpt-5-mini","stream":%s,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"read"}],` +
		`"tools":[{"type":"function","function":{"name":"Read","parameters":{"type":"object","required":["file_path"],"properties":{"file_path":{"type":"string"}}}}}]}`
	respToolsBody = `{"model":"gpt-5-mini","stream":%s,"input":"read",` +
		`"tools":[{"type":"custom","name":"apply_patch"},{"type":"function","name":"shell","parameters":{"type":"object","required":["command"],"properties":{"command":{"type":"array"}}}}]}`
)

// sseData returns the JSON payloads of an SSE body, and whether it ended
// with data: [DONE].
func sseData(t *testing.T, body string) ([]map[string]any, bool) {
	t.Helper()
	var out []map[string]any
	done := false
	for _, frame := range strings.Split(strings.TrimSpace(body), "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			if data == "[DONE]" {
				done = true
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				t.Fatalf("bad sse payload %q: %v", data, err)
			}
			out = append(out, m)
		}
	}
	return out, done
}

func TestScriptedOpenAIChat(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	call := func(stream string) (*http.Response, string) {
		resp, body := env.callAs(t, http.MethodPost, "/v1/chat/completions", "Authorization", "Bearer oa-auth-1",
			strings.Replace(chatToolsBody, "%s", stream, 1))
		return resp, string(body)
	}
	// First request of the run with tools: a tool call.
	_, body := call("false")
	var cc struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct{ Name, Arguments string }
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt  int64 `json:"prompt_tokens"`
			Details struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(body), &cc); err != nil {
		t.Fatal(err)
	}
	tc := cc.Choices[0].Message.ToolCalls
	if !strings.HasPrefix(cc.ID, "chatcmpl-") || cc.Object != "chat.completion" || cc.Choices[0].FinishReason != "tool_calls" ||
		len(tc) != 1 || tc[0].Function.Name != "Read" || tc[0].Function.Arguments != `{"file_path":""}` || !strings.HasPrefix(tc[0].ID, "call_") {
		t.Fatalf("tool turn %s", body)
	}
	last := env.last(t)
	if last.ResponseID != cc.ID || last.Tokens == nil || last.Tokens.CacheRead != cc.Usage.Details.Cached ||
		last.Tokens.Fresh+last.Tokens.CacheRead != cc.Usage.Prompt || cc.Usage.Details.Cached == 0 {
		t.Errorf("ledger %+v tokens %+v usage %+v", last, last.Tokens, cc.Usage)
	}
	// Later requests: text, streamed with a usage chunk and [DONE].
	resp, body := call("true")
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	chunks, done := sseData(t, body)
	if !done || len(chunks) != 4 {
		t.Fatalf("chunks %d done %t: %s", len(chunks), done, body)
	}
	id := chunks[0]["id"].(string)
	for _, c := range chunks {
		if c["id"] != id || c["object"] != "chat.completion.chunk" {
			t.Errorf("chunk %v", c)
		}
	}
	if chunks[3]["usage"] == nil || len(chunks[3]["choices"].([]any)) != 0 || !strings.Contains(body, scriptedText) {
		t.Errorf("stream %s", body)
	}
	if env.last(t).ResponseID != id || !env.last(t).Stream {
		t.Errorf("ledger %+v", env.last(t))
	}
}

func TestScriptedOpenAIResponses(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	_, body := env.callAs(t, http.MethodPost, "/v1/responses", "Authorization", "Bearer oa-auth-1", strings.Replace(respToolsBody, "%s", "true", 1))
	evs, done := sseData(t, string(body))
	if done || len(evs) < 5 {
		t.Fatalf("events %d done %t", len(evs), done)
	}
	var types []string
	for i, ev := range evs {
		types = append(types, ev["type"].(string))
		if int(ev["sequence_number"].(float64)) != i {
			t.Errorf("event %d sequence %v", i, ev["sequence_number"])
		}
	}
	want := "response.created response.in_progress response.output_item.added response.function_call_arguments.delta response.function_call_arguments.done response.output_item.done response.completed"
	if strings.Join(types, " ") != want {
		t.Errorf("event types %v", types)
	}
	if !strings.Contains(string(body), "event: response.completed\n") {
		t.Errorf("events are not named")
	}
	item := evs[5]["item"].(map[string]any)
	if item["type"] != "function_call" || item["name"] != "shell" || item["arguments"] != `{"command":[]}` || !strings.HasPrefix(item["call_id"].(string), "call_") {
		t.Errorf("function call item %v", item)
	}
	completed := evs[6]["response"].(map[string]any)
	id := completed["id"].(string)
	if !strings.HasPrefix(id, "resp_") || completed["usage"] == nil || env.last(t).ResponseID != id {
		t.Errorf("completed %v ledger %+v", completed, env.last(t))
	}
	// Second request: a JSON text reply.
	_, body = env.callAs(t, http.MethodPost, "/v1/responses", "Authorization", "Bearer oa-auth-1", strings.Replace(respToolsBody, "%s", "false", 1))
	var r struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct{ Type, Text string }
		} `json:"output"`
		Usage struct {
			Input   int64 `json:"input_tokens"`
			Details struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Object != "response" || r.Status != "completed" || len(r.Output) != 1 || r.Output[0].Type != "message" ||
		r.Output[0].Content[0].Text != scriptedText || r.Usage.Output != scriptedOutputTokens || r.Usage.Details.Cached != r.Usage.Input/2 {
		t.Errorf("responses json %s", body)
	}
}

func TestScriptedOpenAIAuxiliary(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	cases := []struct {
		method, path, want string
	}{
		{http.MethodPost, "/v1/responses/input_tokens", `"object":"response.input_tokens"`},
		{http.MethodGet, "/v1/models", `"object":"list"`},
		{http.MethodGet, "/v1/models/gpt-6.1-terra", `"id":"gpt-6.1-terra"`},
		{http.MethodPost, "/v1/embeddings", `"type":"invalid_request_error"`},
	}
	for _, tc := range cases {
		_, body := env.callAs(t, tc.method, tc.path, "Authorization", "Bearer oa-auth-1", chatBody)
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("%s %s: %s", tc.method, tc.path, body)
		}
	}
	// A request without function tools never gets a tool call.
	_, body := env.callAs(t, http.MethodPost, "/v1/chat/completions", "Authorization", "Bearer oa-auth-1",
		`{"model":"m","tools":[{"type":"custom","name":"apply_patch"}],"messages":[]}`)
	if strings.Contains(string(body), "tool_calls") {
		t.Errorf("custom-only tools got a tool call: %s", body)
	}
}
