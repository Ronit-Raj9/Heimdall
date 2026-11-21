package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestOwnsModel(t *testing.T) {
	cases := []struct {
		a    *schemas.AgentConfig
		want bool
	}{
		{nil, false},
		{&schemas.AgentConfig{Adapter: "command", Model: "m"}, false},
		{&schemas.AgentConfig{Adapter: "remote_http"}, true},
		{&schemas.AgentConfig{Adapter: "a2a"}, true},
		{&schemas.AgentConfig{Adapter: "openai_agent", OpenAI: &schemas.OpenAIAgent{URL: "u"}}, false},
		{&schemas.AgentConfig{Adapter: "openai_agent", OpenAI: &schemas.OpenAIAgent{URL: "u", Model: "mine"}}, true},
		{&schemas.AgentConfig{Adapter: "openai_agent", Model: "cfg"}, true},
	}
	for i, c := range cases {
		if got := OwnsModel(c.a); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}
