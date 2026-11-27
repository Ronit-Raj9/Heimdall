package bifrost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestOrgProviderBinding checks that a run of an experiment bound to an
// organization picks the organization's slots for a provider it has a key
// for, the shared slots for an unbound experiment, and a refusal for a
// bound experiment the organization has no key for (X01).
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
	// X01: a bound experiment never falls back to the runner's own slots.
	g.BindExperimentOrg("exp_3", "org_nokeys")
	if _, _, err := g.runProvider(rc("exp_3", "anthropic")); !errors.Is(err, ErrNoOrgKey) || !strings.Contains(err.Error(), "Settings > Model keys") {
		t.Fatalf("bound run with no org key: %v, want ErrNoOrgKey", err)
	}
	practice := rc("exp_3", "anthropic")
	practice.Experiment = &schemas.Experiment{Smoke: true}
	if p, _, err := g.runProvider(practice); err != nil || p.Slots[0].Name != "fp-slot-1" {
		t.Fatalf("practice run (scripted model only) must keep the shared slots: %+v %v", p, err)
	}
	oracle := rc("exp_3", "anthropic")
	oracle.Agent.Adapter, oracle.Agent.Scripted = "scripted", &schemas.ScriptedSpec{Kind: "oracle"}
	if _, _, err := g.runProvider(oracle); err != nil {
		t.Fatalf("a scripted control that makes no model call: %v", err)
	}
	remote := rc("exp_3", "anthropic")
	remote.Agent.Remote = &schemas.RemoteAgent{URL: "https://agent.example"}
	if _, _, err := g.runProvider(remote); err != nil {
		t.Fatalf("a deployed agent with external model traffic: %v", err)
	}
	remote.Agent.Remote.ModelTraffic = "gateway"
	if _, _, err := g.runProvider(remote); !errors.Is(err, ErrNoOrgKey) {
		t.Fatalf("a deployed agent routed through the gateway: %v, want ErrNoOrgKey", err)
	}
	toy := rc("exp_3", "anthropic")
	toy.Agent.Adapter, toy.Agent.Scripted = "scripted", &schemas.ScriptedSpec{Kind: "toy"}
	if _, _, err := g.runProvider(toy); !errors.Is(err, ErrNoOrgKey) {
		t.Fatalf("the toy agent calls the model: %v, want ErrNoOrgKey", err)
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

// fakeOrgRegistrar records RegisterOrgSlots; the other claim calls are not
// used by ApplyOrgCredentials.
type fakeOrgRegistrar struct {
	SlotClaimer
	got []OrgSlot
}

func (f *fakeOrgRegistrar) RegisterOrgSlots(_ context.Context, slots []OrgSlot) error {
	f.got = append([]OrgSlot(nil), slots...)
	return nil
}

// TestApplyOrgCredentialsRoundRobin checks G10: every key of a provider is
// delivered, the org slots alternate between them, each slot knows its key
// id, and successive runs start their slot search on the next slot.
func TestApplyOrgCredentialsRoundRobin(t *testing.T) {
	reg := &fakeOrgRegistrar{}
	g, err := New(Config{URL: "http://bifrost", SetupToken: "t", FaultProxyURL: "http://fp:9100", Claims: reg,
		Providers: []ProviderSetup{{Name: "anthropic", Model: "m", Slots: []Slot{{Name: "s1", Value: "s1"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	// The provider's org slots are already in Bifrost (same count and model),
	// so the call only re-registers keys at the fault proxy.
	known := ProviderSetup{Name: "anthropic", Mode: ModeProxied, Model: "m"}
	for i := 1; i <= orgSlotsPerProvider; i++ {
		n := OrgSlotName("org_a", "anthropic", i)
		known.Slots = append(known.Slots, Slot{Name: n, Value: n})
	}
	g.orgs().byOrg["org_a"] = map[string]ProviderSetup{"anthropic": known}

	creds := []ProviderCredential{
		{KeyID: "key_1", Provider: "anthropic", Value: "sk-one"},
		{KeyID: "key_2", Provider: "anthropic", Value: "sk-two"},
		{KeyID: "key_dup", Provider: "anthropic", Value: "sk-one"}, // same value: not a second key
	}
	if err := g.ApplyOrgCredentials(context.Background(), "org_a", creds); err != nil {
		t.Fatal(err)
	}
	if len(reg.got) != orgSlotsPerProvider {
		t.Fatalf("registered %d slots", len(reg.got))
	}
	for i, s := range reg.got {
		if want := []string{"sk-one", "sk-two"}[i%2]; s.Key != want { // round robin over the two distinct keys
			t.Fatalf("slot %d key %q", i+1, s.Key)
		}
	}
	if id := g.orgSlotKeyID(OrgSlotName("org_a", "anthropic", 2)); id != "key_2" {
		t.Fatalf("slot 2 key id %q", id)
	}
	if id := g.orgSlotKeyID("s1"); id != "" {
		t.Fatalf("shared slot key id %q", id)
	}
	g.BindExperimentOrg("exp_1", "org_a")
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_abc", ExpID: "exp_1"}, Agent: &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Provider: "anthropic"}}}
	first := map[string]bool{}
	for i := 0; i < orgSlotsPerProvider; i++ {
		p, _, err := g.runProvider(rc)
		if err != nil || len(p.Slots) != orgSlotsPerProvider {
			t.Fatalf("run %d: %+v %v", i, p, err)
		}
		first[p.Slots[0].Name] = true
	}
	if len(first) != orgSlotsPerProvider {
		t.Fatalf("runs did not rotate over the slots: %v", first)
	}
}
