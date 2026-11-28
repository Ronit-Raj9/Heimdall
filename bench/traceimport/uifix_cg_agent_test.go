package traceimport

import (
	"strings"
	"testing"
)

// TestOTLPAgent: the session's agent is the span's gen_ai.agent.name, else
// the resource's service.name; none when the trace names neither.
func TestOTLPAgent(t *testing.T) {
	doc := func(resource, spanAgent string) []byte {
		res := `{}`
		if resource != "" {
			res = `{"attributes":[{"key":"service.name","value":{"stringValue":"` + resource + `"}}]}`
		}
		attrs := `[]`
		if spanAgent != "" {
			attrs = `[{"key":"gen_ai.agent.name","value":{"stringValue":"` + spanAgent + `"}}]`
		}
		return []byte(`{"resourceSpans":[{"resource":` + res + `,"scopeSpans":[{"spans":[{"traceId":"aaaaaaaaaaaaaaaa1111111111111111","spanId":"aaaaaaaa0001","name":"chat","attributes":` + attrs + `}]}]}]}`)
	}
	for _, c := range []struct{ res, span, want string }{
		{"support-bot", "", "support-bot"},
		{"support-bot", "planner", "planner"},
		{"", "", ""},
	} {
		tr, err := Parse(doc(c.res, c.span), "otlp")
		if err != nil || len(tr.Sessions) != 1 {
			t.Fatalf("parse: %v", err)
		}
		if got := tr.Sessions[0].Agent; got != c.want {
			t.Fatalf("%+v: agent %q", c, got)
		}
	}
}

// TestAgentNameCleaned: the trace-given agent name loses control and format
// characters, keeps one line and is cut to MaxAgentBytes.
func TestAgentNameCleaned(t *testing.T) {
	if got := agentName("evil\u202etob\x1b[31m\x00 bot\n2"); got != "eviltob[31m bot 2" {
		t.Fatalf("agent %q", got)
	}
	if got := agentName(strings.Repeat("a", 500)); len(got) > MaxAgentBytes+3 {
		t.Fatalf("agent not bounded: %d bytes", len(got))
	}
}
