package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestOpenAIEndpoint(t *testing.T) {
	for _, tc := range []struct {
		o    schemas.OpenAIAgent
		want string
	}{
		{schemas.OpenAIAgent{URL: "https://a.example.com/v1"}, "https://a.example.com/v1/chat/completions"},
		{schemas.OpenAIAgent{URL: "https://a.example.com/v1/"}, "https://a.example.com/v1/chat/completions"},
		{schemas.OpenAIAgent{URL: "https://a.example.com/v1", API: "responses"}, "https://a.example.com/v1/responses"},
		{schemas.OpenAIAgent{URL: "http://h:8000/x/chat/completions"}, "http://h:8000/x/chat/completions"},
		{schemas.OpenAIAgent{URL: "http://h:8000/responses", API: "responses"}, "http://h:8000/responses"},
	} {
		got, err := OpenAIEndpoint(&tc.o)
		if err != nil || got != tc.want {
			t.Errorf("%+v: %q %v, want %q", tc.o, got, err, tc.want)
		}
	}
}

func TestOpenAISecretAndTraffic(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Adapter: AdapterOpenAIAgent, OpenAI: &schemas.OpenAIAgent{URL: "https://x/v1", APIKeySecret: "K1"},
		Env: map[string]string{"X": "{{secret.K2}}"}}}
	if got := RunSecretNames(rc); len(got) != 2 || got[0] != "K1" || got[1] != "K2" {
		t.Fatalf("secret names %v", got)
	}
	log := &schemas.RequestLog{Complete: true}
	MarkExternalTraffic(rc, log)
	if log.Complete || log.Reason != OpenAIExternalReason {
		t.Fatalf("log %+v", log)
	}
}
