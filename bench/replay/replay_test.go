package replay

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestRequestHashIgnoresNoise(t *testing.T) {
	a := `{"model":"m","metadata":{"user_id":"u1"},"messages":[{"role":"user","content":"ran at 2026-11-20T10:00:01Z in /tmp/abc123 id 0b1f3c3e-1111-2222-3333-444455556666 took 1.2s"}]}`
	b := `{"messages":[{"role":"user","content":"ran at 2026-11-21T11:30:09Z in /tmp/zzz999 id 9b1f3c3e-aaaa-2222-3333-444455556666 took 3.9s"}],"model":"m","metadata":{"user_id":"u2"}}`
	if RequestHash([]byte(a)) != RequestHash([]byte(b)) {
		t.Fatal("noise changed the hash")
	}
	c := strings.Replace(a, "ran at", "failed at", 1)
	if RequestHash([]byte(a)) == RequestHash([]byte(c)) {
		t.Fatal("a real change kept the hash")
	}
	if RequestHash(nil) != "" {
		t.Fatal("empty body must hash empty")
	}
	if RequestHash([]byte("not json")) != utils.SHA256Bytes([]byte("not json")) {
		t.Fatal("non-JSON hashes as raw bytes")
	}
}

func rawLine(t *testing.T, id, req, resp string, stream bool, status string) string {
	t.Helper()
	d, _ := json.Marshal(map[string]any{"raw_request": req, "raw_response": resp, "stream": stream, "model": "claude-x", "status": status})
	b, _ := json.Marshal(map[string]any{"request_id": id, "raw_available": true, "raw_detail": json.RawMessage(d)})
	return string(b)
}

func TestBuildFromLedgerAndRaw(t *testing.T) {
	r1 := `{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`
	r2 := `{"model":"claude-x","messages":[{"role":"user","content":"again"}]}`
	stream := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	raw := ParseRaw([]byte(strings.Join([]string{
		rawLine(t, "a", r1, `{"id":"msg_1","content":[]}`, false, "success"),
		rawLine(t, "b", r2, stream, true, "success"),
	}, "\n")))
	if len(raw) != 2 {
		t.Fatalf("raw rows = %d", len(raw))
	}
	ledger := []schemas.LedgerEntry{
		{RunID: "r", Kind: "message", Call: 2, Action: "pass", Status: 200, Stream: true, ReqSHA256: utils.SHA256Bytes([]byte(r2))},
		{RunID: "r", Kind: "count_tokens", Call: 0, Action: "pass"},
		{RunID: "r", Kind: "message", Call: 1, Action: "inject", Status: 503, ReqSHA256: utils.SHA256Bytes([]byte(r1))},
		{RunID: "r", Kind: "message", Call: 3, Action: "pass", Status: 200, ReqSHA256: utils.SHA256Bytes([]byte(r1))},
	}
	c, err := Build("r", ledger, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 3 {
		t.Fatalf("entries = %d", len(c.Entries))
	}
	if e := c.Entries[0]; e.Call != 1 || e.Missing != MissingFault || e.Status != 503 || !Usable(e) {
		t.Fatalf("call 1 = %+v", e)
	}
	if e := c.Entries[1]; e.Call != 2 || !e.Stream || e.Body != `{"type":"message_start"}`+"\n\n"+`{"type":"message_stop"}` {
		t.Fatalf("call 2 = %+v", e)
	}
	if e := c.Entries[2]; e.Body != `{"id":"msg_1","content":[]}` || e.ReqHash != RequestHash([]byte(r1)) {
		t.Fatalf("call 3 = %+v", e)
	}
}

func TestBuildWithoutLedgerAndEmpty(t *testing.T) {
	raw := ParseRaw([]byte(rawLine(t, "a", `{"a":1}`, "", false, "error")))
	c, err := Build("r", nil, raw)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Entries[0]; e.Missing != MissingNoBody || e.Status != 500 || !Usable(e) {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := Build("r", nil, nil); !errors.Is(err, ErrNothingToReplay) {
		t.Fatalf("err = %v", err)
	}
	ok := ParseRaw([]byte(rawLine(t, "a", `{"a":1}`, "", false, "success")))
	c, _ = Build("r", nil, ok)
	if Usable(c.Entries[0]) {
		t.Fatal("a 200 without a body cannot answer a call")
	}
}

func TestStreamPayloads(t *testing.T) {
	got := StreamPayloads("data: {\"a\":1}\n\ndata: [DONE]\n\n")
	if len(got) != 1 || got[0] != `{"a":1}` {
		t.Fatalf("payloads = %q", got)
	}
	got = StreamPayloads("{\"a\":1}\n{\"b\":2}")
	if len(got) != 2 {
		t.Fatalf("bare lines = %q", got)
	}
	if EventType(`{"type":"message_stop"}`) != "message_stop" || EventType("x") != "" {
		t.Fatal("event type")
	}
}

func TestAppendUserMessage(t *testing.T) {
	cases := []struct{ shape, in, want string }{
		{ShapeAnthropic, `{"messages":[{"role":"assistant","content":"x"}]}`, `{"messages":[{"content":"x","role":"assistant"},{"content":"look again","role":"user"}]}`},
		{ShapeAnthropic, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t"}]}]}`, `{"messages":[{"content":[{"tool_use_id":"t","type":"tool_result"},{"text":"look again","type":"text"}],"role":"user"}]}`},
		{ShapeChat, `{"messages":[]}`, `{"messages":[{"content":"look again","role":"user"}]}`},
		{ShapeResponses, `{"input":"start"}`, `{"input":[{"content":"start","role":"user"},{"content":"look again","role":"user"}]}`},
		{ShapeGemini, `{}`, `{"contents":[{"parts":[{"text":"look again"}],"role":"user"}]}`},
	}
	for _, c := range cases {
		got, err := AppendUserMessage([]byte(c.in), c.shape, "look again")
		if err != nil {
			t.Fatalf("%s: %v", c.shape, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s: got %s", c.shape, got)
		}
	}
	if _, err := AppendUserMessage([]byte(`{}`), "other", "x"); !errors.Is(err, ErrUnknownShape) {
		t.Fatal("unknown shape")
	}
}

func TestNewToolResults(t *testing.T) {
	anth := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash"},{"type":"tool_use","id":"t2","name":"edit"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2"}]}]}`
	if got := NewToolResults([]byte(anth), ShapeAnthropic); len(got) != 1 || got[0] != "edit" {
		t.Fatalf("anthropic = %v", got)
	}
	chat := `{"messages":[{"role":"assistant","tool_calls":[{"id":"c1","function":{"name":"read"}}]},{"role":"tool","tool_call_id":"c1"}]}`
	if got := NewToolResults([]byte(chat), ShapeChat); len(got) != 1 || got[0] != "read" {
		t.Fatalf("chat = %v", got)
	}
	resp := `{"input":[{"type":"function_call","call_id":"f","name":"shell"},{"type":"function_call_output","call_id":"f"}]}`
	if got := NewToolResults([]byte(resp), ShapeResponses); len(got) != 1 || got[0] != "shell" {
		t.Fatalf("responses = %v", got)
	}
	if got := NewToolResults([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), ShapeAnthropic); len(got) != 0 {
		t.Fatalf("no tool results = %v", got)
	}
}
