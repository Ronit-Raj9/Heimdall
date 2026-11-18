package gates

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X8 voice agents: audio rows for the agent's declared models never fire
// the gate; an audio row served by another model does.
func TestModelFidelityAudioRows(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "gpt-4o-mini", Gateway: schemas.GatewayPolicy{Provider: "openai", Models: []string{"whisper-1", "gpt-4o-mini-tts"}}}}
	audio := func(id, requested, served string) schemas.GatewayRequest {
		return schemas.GatewayRequest{ID: id, Kind: "audio", ModelRequested: requested, ModelServed: served}
	}
	g := newModelFidelityGate()
	ok := []schemas.GatewayRequest{
		{ID: "c1", Kind: "message", ModelRequested: "openai/gpt-4o-mini", ModelServed: "gpt-4o-mini"},
		audio("a1", "openai/whisper-1", "whisper-1"),
		audio("a2", "gpt-4o-mini-tts", "gpt-4o-mini-tts"),
		audio("a3", "whisper-1", ""), // an injected STT outage: no served model
	}
	res, err := g.Check(context.Background(), rc, &schemas.Evidence{Requests: &schemas.RequestLog{Requests: ok}})
	if err != nil || res.Fired {
		t.Fatalf("declared audio rows fired: %+v %v", res, err)
	}
	bad := append(ok, audio("a4", "whisper-1", "gpt-4o-transcribe"))
	res, err = g.Check(context.Background(), rc, &schemas.Evidence{Requests: &schemas.RequestLog{Requests: bad}})
	if err != nil || !res.Fired || len(res.Evidence) != 1 || res.Evidence[0] != "a4" {
		t.Fatalf("swapped audio model: %+v %v", res, err)
	}
}
