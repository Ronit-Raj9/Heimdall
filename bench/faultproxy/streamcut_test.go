package faultproxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// streamCall posts body and reads the answer to the end, returning what
// arrived and the read error (a cut stream ends in an unexpected EOF).
func streamCall(t *testing.T, env *testEnv, path, header, auth, body string) (string, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.data.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(header, auth)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, rerr := io.ReadAll(resp.Body)
	return string(b), rerr
}

func TestStreamCutScriptedAnthropic(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeStreamCut, Call: 1, RunID: "r1", Nonce: "nonce_cut"})
	stream := strings.Replace(toolsBody, `"max_tokens":64,`, `"max_tokens":64,"stream":true,`, 1)
	got, err := streamCall(t, env, "/v1/messages", "x-api-key", "auth-a", stream)
	if err == nil {
		t.Fatalf("stream was not cut: %s", got)
	}
	if !strings.Contains(got, "input_json_delta") || strings.Contains(got, "message_stop") || strings.Contains(got, "content_block_stop") {
		t.Fatalf("cut at the wrong place: %s", got)
	}
	e := env.last(t)
	if e.Action != ActionInject || e.Nonce != "nonce_cut" || e.Status != 200 || !e.Stream || e.Call != 1 {
		t.Fatalf("ledger line %+v", e)
	}
	// Call 2 is not taken: it streams whole.
	got, err = streamCall(t, env, "/v1/messages", "x-api-key", "auth-a", stream)
	if err != nil || !strings.Contains(got, "message_stop") {
		t.Fatalf("second stream %v %s", err, got)
	}
	if e := env.last(t); e.Action != ActionPass {
		t.Fatalf("second line %+v", e)
	}
	env.entries(t) // the chain verifies
}

func TestStreamCutPassesWithoutToolCall(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeStreamCut, FromCall: 1, RunID: "r1", Nonce: "nonce_cut"})
	stream := strings.Replace(msgBody, `"max_tokens":64,`, `"max_tokens":64,"stream":true,`, 1)
	got, err := streamCall(t, env, "/v1/messages", "x-api-key", "auth-a", stream)
	if err != nil || !strings.Contains(got, "message_stop") {
		t.Fatalf("text stream must pass whole: %v %s", err, got)
	}
	if e := env.last(t); e.Action != ActionPass {
		t.Fatalf("ledger line %+v", e)
	}
	// A request that does not stream is never cut.
	resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", toolsBody)
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("tool_use")) {
		t.Fatalf("non-stream %d %s", resp.StatusCode, body)
	}
}

func TestStreamCutRealUpstreamOpenAI(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, f := range frames {
			_, _ = io.WriteString(w, "data: "+f+"\n\n")
			w.(http.Flusher).Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	var providers []ProviderSpec
	for _, p := range DefaultProviders() {
		p.BaseURL = up.URL // never the real API
		providers = append(providers, p)
	}
	env := newTestEnvConfig(t, Config{Providers: providers, Getenv: func(k string) string {
		if k == "OPENAI_API_KEY" {
			return "sk-test"
		}
		return ""
	}, Slots: []SlotConfig{{Name: "oa-1", Auth: "oa-auth-1", Provider: ProviderOpenAI}}})
	env.setPlan(t, "oa-1", schemas.FaultPlan{Mode: ModeStreamCut, Call: 1, RunID: "r2", Nonce: "nonce_oa"})
	got, err := streamCall(t, env, "/v1/chat/completions", "Authorization", "Bearer oa-auth-1", `{"model":"gpt-x","stream":true,"messages":[]}`)
	if err == nil {
		t.Fatalf("stream not cut: %s", got)
	}
	if !strings.Contains(got, `\"comm`) || strings.Contains(got, `ls\"`) || strings.Contains(got, "[DONE]") {
		t.Fatalf("cut at the wrong place: %s", got)
	}
	if e := env.last(t); e.Action != ActionInject || e.Nonce != "nonce_oa" {
		t.Fatalf("ledger line %+v", e)
	}
}

func TestValidatePlanStreamCut(t *testing.T) {
	tests := []struct {
		name string
		p    schemas.FaultPlan
		ok   bool
	}{
		{"call", schemas.FaultPlan{Mode: ModeStreamCut, Call: 2}, true},
		{"from_call", schemas.FaultPlan{Mode: ModeStreamCut, FromCall: 1}, true},
		{"neither", schemas.FaultPlan{Mode: ModeStreamCut}, false},
		{"status set", schemas.FaultPlan{Mode: ModeStreamCut, Call: 1, Status: 503}, false},
	}
	for _, tt := range tests {
		if err := ValidatePlan(tt.p); (err == nil) != tt.ok {
			t.Errorf("%s: err %v", tt.name, err)
		}
	}
}

func TestToolDelta(t *testing.T) {
	tests := []struct {
		format, ev string
		want       bool
	}{
		{FormatAnthropic, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}\n\n", true},
		{FormatAnthropic, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n", false},
		{FormatOpenAI, "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\"}\n\n", true},
		{FormatOpenAI, "data: [DONE]\n\n", false},
		{FormatGemini, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"x\"}}]}}]}\r\n\r\n", true},
	}
	for _, tt := range tests {
		if got := toolDelta(tt.format, []byte(tt.ev)); got != tt.want {
			t.Errorf("%q: got %v", tt.ev, got)
		}
	}
}
