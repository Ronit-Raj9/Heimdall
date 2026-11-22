package agentevents

import (
	"testing"
	"time"
)

// TestSpanTypeModelWithoutOperationIsLLM: a span that names a model but no
// GenAI operation (older conventions, many OpenAI instrumentations) is a
// model call; a tool span with a model attribute stays a tool call (N109).
func TestSpanTypeModelWithoutOperationIsLLM(t *testing.T) {
	cases := []struct {
		attrs map[string]string
		want  string
	}{
		{map[string]string{"gen_ai.system": "openai", "gen_ai.request.model": "gpt-4o-mini"}, TypeLLM},
		{map[string]string{"gen_ai.response.model": "gpt-4o-mini-2024-07-18"}, TypeLLM},
		{map[string]string{"llm.model_name": "claude-x"}, TypeLLM},
		{map[string]string{"gen_ai.tool.name": "search", "gen_ai.request.model": "gpt-4o-mini"}, TypeToolCall},
		{map[string]string{"tool.name": "search", "gen_ai.request.model": "gpt-4o-mini"}, TypeSpan},
		{map[string]string{"gen_ai.system": "openai"}, TypeSpan},
	}
	for _, c := range cases {
		if got := spanType(span{name: "openai.chat", attrs: c.attrs}); got != c.want {
			t.Errorf("spanType(%v) = %s, want %s", c.attrs, got, c.want)
		}
	}
	body := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"0102030405060708","name":"openai.chat","attributes":[{"key":"gen_ai.system","value":{"stringValue":"openai"}},{"key":"gen_ai.request.model","value":{"stringValue":"gpt-4o-mini"}}]}]}]}]}`)
	recs, _, err := ParseOTLP(body, ContentTypeOTLPJSON, Limits{}, time.Now())
	if err != nil || len(recs) != 1 || recs[0].Type != TypeLLM || recs[0].Model != "gpt-4o-mini" {
		t.Fatalf("ParseOTLP: %v %+v", err, recs)
	}
}
