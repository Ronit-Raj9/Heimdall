package bifrost

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestOrgProviderBinding checks that a run of an experiment bound to an
// organization picks the organization's slots for a provider it has a key
// for, and the shared slots otherwise.
func TestOrgProviderBinding(t *testing.T) {
	g, err := New(Config{URL: "http://bifrost", SetupToken: "t", FaultProxyURL: "http://fp:9100",
		Providers: []ProviderSetup{{Name: "anthropic", Model: "claude-sonnet-4-5", Slots: []Slot{{Name: "fp-slot-1", Value: "fp-slot-1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	st := g.orgs()
	org := ProviderSetup{Name: "anthropic", Mode: ModeProxied, Model: "claude-sonnet-4-5",
		Slots: []Slot{{Name: OrgSlotName("org_A1", "anthropic", 1), Value: "x"}}}
	st.byOrg["org_A1"] = map[string]ProviderSetup{"anthropic": org}
	g.BindExperimentOrg("exp_1", "org_A1")

	rc := func(exp, provider string) *schemas.RunContext {
		return &schemas.RunContext{Run: &schemas.Run{RunID: "r_abc", ExpID: exp},
			Agent: &schemas.AgentConfig{Model: "claude-haiku-4-5", Gateway: schemas.GatewayPolicy{Provider: provider}}}
	}
	p, model, err := g.runProvider(rc("exp_1", "anthropic"))
	if err != nil || model != "claude-haiku-4-5" || len(p.Slots) != 1 || !strings.HasPrefix(p.Slots[0].Name, "org_orga1_anthropic_") {
		t.Fatalf("bound run: %+v %s %v", p, model, err)
	}
	if p, _, _ := g.runProvider(rc("exp_2", "anthropic")); p.Slots[0].Name != "fp-slot-1" {
		t.Fatalf("unbound run took %s", p.Slots[0].Name)
	}
	if _, _, err := g.runProvider(rc("exp_1", "openai")); err == nil {
		t.Fatal("a provider neither the org nor the gateway has was accepted")
	}
	if sp, ok := g.slotProvider(org.Slots[0].Name); !ok || sp.Name != "anthropic" {
		t.Fatal("slotProvider does not know the org slot")
	}
}

// TestApplyOrgCredentialsNeedsFaultProxy checks the refusal without a fault
// proxy control client, and that empty input is a no-op.
func TestApplyOrgCredentialsNeedsFaultProxy(t *testing.T) {
	g, err := New(Config{URL: "http://bifrost", SetupToken: "t", FaultProxyURL: "http://fp:9100",
		Providers: []ProviderSetup{{Name: "anthropic", Model: "m", Slots: []Slot{{Name: "s1", Value: "s1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := g.ApplyOrgCredentials(ctx, "org_a", nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	err = g.ApplyOrgCredentials(ctx, "org_a", []ProviderCredential{{Provider: "anthropic", Value: "sk-secret-value"}})
	if err == nil || strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("err %v", err)
	}
	if err := g.ApplyOrgCredentials(ctx, " ", []ProviderCredential{{Provider: "anthropic", Value: "k"}}); err == nil {
		t.Fatal("empty org accepted")
	}
}
