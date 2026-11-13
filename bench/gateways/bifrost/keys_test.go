package bifrost

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMintAndRevokeKey(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	ctx := context.Background()
	if _, err := g.MintKey(ctx, runContext("r_before01", testModel)); err == nil || !strings.Contains(err.Error(), "Setup has not run") {
		t.Fatalf("mint before Setup: err = %v", err)
	}
	if err := g.Setup(ctx); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	k1, err := g.MintKey(ctx, runContext("r_run00001", testModel))
	if err != nil {
		t.Fatalf("mint 1: %v", err)
	}
	k2, err := g.MintKey(ctx, runContext("r_run00002", ""))
	if err != nil {
		t.Fatalf("mint 2: %v", err)
	}
	if k1.KeySlot != "fp-slot-1" || k1.SlotAuth != "slot-auth-1" || k2.KeySlot != "fp-slot-2" {
		t.Errorf("slots = %s %s", k1.KeySlot, k2.KeySlot)
	}
	if k1.Name != "r_run00001" || k1.Value == "" || k1.Provider != "anthropic" || k2.Model != testModel {
		t.Errorf("key = %+v", k1)
	}
	body := f.lastBody("POST /api/governance/virtual-keys")
	pcs, _ := body["provider_configs"].([]any)
	pc, _ := pcs[0].(map[string]any)
	if body["is_active"] != true || pc["weight"] != float64(1) || pc["provider"] != "anthropic" ||
		pc["key_ids"].([]any)[0] != g.keyIDs["fp-slot-2"] || pc["allowed_models"].([]any)[0] != testModel {
		t.Errorf("virtual key body = %v", body)
	}
	if _, err := g.MintKey(ctx, runContext("r_run00003", testModel)); err == nil || !strings.Contains(err.Error(), "no free key slot") {
		t.Fatalf("third mint: err = %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := g.RevokeKey(ctx, k1); err != nil {
			t.Fatalf("revoke %d: %v", i, err)
		}
	}
	k3, err := g.MintKey(ctx, runContext("r_run00003", testModel))
	if err != nil {
		t.Fatalf("mint after revoke: %v", err)
	}
	if k3.KeySlot != "fp-slot-1" {
		t.Errorf("freed slot not reused: %s", k3.KeySlot)
	}
	// A stale RunKey for slot 1 must not free the slot run 3 holds.
	if err := g.RevokeKey(ctx, k1); err != nil {
		t.Fatalf("stale revoke: %v", err)
	}
	if g.inUse["fp-slot-1"] != k3.ID {
		t.Errorf("stale revoke released slot: %v", g.inUse)
	}
	if err := g.RevokeKey(ctx, nil); err != nil {
		t.Errorf("revoke nil: %v", err)
	}
}

func TestMintKeyRefusals(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	if err := g.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	other := runContext("r_run00001", testModel)
	other.Agent.Gateway.Provider = "openai"
	tests := []struct {
		name    string
		rc      *schemas.RunContext
		wantErr string
	}{
		{name: "nil run", rc: &schemas.RunContext{}, wantErr: "no run id"},
		{name: "not a run id", rc: runContext("spike-vk-a", testModel), wantErr: "does not match"},
		{name: "other provider", rc: other, wantErr: "not the gateway provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := g.MintKey(context.Background(), tt.rc)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if len(g.inUse) != 0 {
				t.Errorf("refused mint holds a slot: %v", g.inUse)
			}
		})
	}
}

func TestMintKeyAddsRunModelToSlot(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	if err := g.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	puts := f.count("PUT /api/providers/{p}/keys/{id}")
	k, err := g.MintKey(context.Background(), runContext("r_run00001", "claude-haiku-5-5"))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if k.Model != "claude-haiku-5-5" || f.count("PUT /api/providers/{p}/keys/{id}") != puts+1 {
		t.Fatalf("model %s, key puts %d", k.Model, f.count("PUT /api/providers/{p}/keys/{id}"))
	}
	models := f.lastBody("PUT /api/providers/{p}/keys/{id}")["models"].([]any)
	if len(models) != 2 || models[0] != "claude-haiku-5-5" && models[1] != "claude-haiku-5-5" {
		t.Errorf("slot models = %v", models)
	}
}

func TestRevokeStale(t *testing.T) {
	f := newFakeBifrost(t)
	f.vks = []map[string]any{
		{"id": "a", "name": "r_active01"},
		{"id": "b", "name": "r_stale001"},
		{"id": "c", "name": "spike-vk-slot-a"},
		{"id": "d", "name": "r_stale002"},
	}
	g := newTestGateway(t, f)
	n, err := g.RevokeStale(context.Background(), map[string]bool{"r_active01": true})
	if err != nil {
		t.Fatalf("RevokeStale: %v", err)
	}
	if n != 2 || len(f.vks) != 2 || f.vks[0]["name"] != "r_active01" || f.vks[1]["name"] != "spike-vk-slot-a" {
		t.Errorf("n = %d, left %v", n, f.vks)
	}
}
