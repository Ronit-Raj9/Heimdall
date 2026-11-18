package bifrost

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// X8 voice agents: gateway.models are added to the slot and to the run
// key's allowed models, after the arm's model.
func TestMintKeyAddsDeclaredModels(t *testing.T) {
	f := newFakeBifrost(t)
	g, err := New(multiConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	rc := runContext("r_voice0001", "gpt-4o-mini")
	rc.Agent.Gateway.Provider = "openai"
	rc.Agent.Gateway.Models = []string{"whisper-1", "gpt-4o-mini-tts", "gpt-4o-mini"}
	k, err := g.MintKey(context.Background(), rc)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if k.Model != "gpt-4o-mini" {
		t.Fatalf("key model %s", k.Model)
	}
	vk := f.lastBody("POST /api/governance/virtual-keys")
	pc := vk["provider_configs"].([]any)[0].(map[string]any)
	if got := fmt.Sprint(pc["allowed_models"]); got != "[gpt-4o-mini whisper-1 gpt-4o-mini-tts]" {
		t.Fatalf("allowed models %s", got)
	}
	slot := fmt.Sprint(f.lastBody("PUT /api/providers/{p}/keys/{id}")["models"])
	for _, m := range []string{"whisper-1", "gpt-4o-mini-tts", "gpt-4o-mini"} {
		if !strings.Contains(slot, m) {
			t.Errorf("slot models %s lack %s", slot, m)
		}
	}
	// Without the field the key keeps its single model.
	plain := runContext("r_voice0002", "gpt-4o-mini")
	plain.Agent.Gateway.Provider = "openai"
	if _, err := g.MintKey(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	pc = f.lastBody("POST /api/governance/virtual-keys")["provider_configs"].([]any)[0].(map[string]any)
	if got := fmt.Sprint(pc["allowed_models"]); got != "[gpt-4o-mini]" {
		t.Fatalf("plain key allowed models %s", got)
	}
}

func TestAudioRowKinds(t *testing.T) {
	for object, want := range map[string]string{"speech": kindAudio, "transcription": kindAudio, "realtime.turn": kindAudio, "chat_completion": kindMessage, "image_generation": kindOther} {
		if got := kindOf(object); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", object, got, want)
		}
	}
}

func TestAddAllowedModels(t *testing.T) {
	body := map[string]any{"provider_configs": []map[string]any{{"allowed_models": []string{"a"}}}}
	addAllowedModels(body, []string{"b", "a", "b"})
	if got := fmt.Sprint(body["provider_configs"].([]map[string]any)[0]["allowed_models"]); got != "[a b]" {
		t.Fatalf("allowed %s", got)
	}
	addAllowedModels(map[string]any{}, []string{"x"}) // no provider configs: nothing to do, no panic
}

// X8 voice agents: a slot that serves the declared audio models after a
// voice run's key was minted is still clean for the hygiene preflight
// (extra models on a slot move no traffic; only aliases and routing do).
func TestHygieneAfterDeclaredModels(t *testing.T) {
	f := newFakeBifrost(t)
	g, err := New(multiConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	rc := runContext("r_voice0003", "gpt-4o-mini")
	rc.Agent.Gateway.Provider = "openai"
	rc.Agent.Gateway.Models = []string{"whisper-1", "gpt-4o-mini-tts"}
	if _, err := g.MintKey(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	m, err := g.Preflight(context.Background())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(m.Hygiene.Violations) != 0 {
		t.Fatalf("hygiene violations after declared models: %v", m.Hygiene.Violations)
	}
}
