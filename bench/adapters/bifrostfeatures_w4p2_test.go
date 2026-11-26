package adapters

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBifrostHeadersIncludeGatewayHeaders(t *testing.T) {
	rc := &schemas.RunContext{
		Agent:          &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Bifrost: &schemas.BifrostFeatures{Prompt: &schemas.BifrostPrompt{Name: "p"}}}},
		GatewayHeaders: map[string]string{"x-bf-prompt-id": "p-1", "x-bf-prompt-version": "1"},
	}
	h := BifrostHeaders(rc)
	if h["x-bf-prompt-id"] != "p-1" || h["x-bf-prompt-version"] != "1" {
		t.Fatalf("headers = %v", h)
	}
}
