package bifrost

import (
	"context"
	"slices"
	"testing"
)

// fakeOrgReleaser records the G3 B7 calls: replace prefixes and releases.
type fakeOrgReleaser struct {
	fakeOrgRegistrar
	replaced [][]string
	released [][]string
}

func (f *fakeOrgReleaser) ReplaceOrgSlots(_ context.Context, slots []OrgSlot, replace []string) error {
	f.got = append([]OrgSlot(nil), slots...)
	f.replaced = append(f.replaced, replace)
	return nil
}

func (f *fakeOrgReleaser) ReleaseOrgSlots(_ context.Context, prefixes []string) error {
	f.released = append(f.released, prefixes)
	return nil
}

// TestOrgCredentialsFreeSlots checks G3 B7 on the gateway side: a provider
// registration replaces the provider's earlier slots, a provider whose keys
// are all gone is dropped, and ReleaseOrgCredentials frees the whole
// organization at the fault proxy and in the gateway's state.
func TestOrgCredentialsFreeSlots(t *testing.T) {
	reg := &fakeOrgReleaser{}
	g, err := New(Config{URL: "http://bifrost", SetupToken: "t", FaultProxyURL: "http://fp:9100", Claims: reg,
		Providers: []ProviderSetup{
			{Name: "anthropic", Model: "m", Slots: []Slot{{Name: "s1", Value: "s1"}}},
			{Name: "openai", Model: "m", Slots: []Slot{{Name: "o1", Value: "o1"}}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	st := g.orgs()
	for _, p := range []string{"anthropic", "openai"} {
		known := ProviderSetup{Name: p, Mode: ModeProxied, Model: "m"}
		for i := 1; i <= orgSlotsPerProvider; i++ {
			n := OrgSlotName("org_a", p, i)
			known.Slots = append(known.Slots, Slot{Name: n, Value: n})
			st.slotKey[n] = "key_" + p
		}
		if st.byOrg["org_a"] == nil {
			st.byOrg["org_a"] = map[string]ProviderSetup{}
		}
		st.byOrg["org_a"][p] = known
	}
	g.BindExperimentOrg("exp_1", "org_a")
	ctx := context.Background()
	if err := g.ApplyOrgCredentials(ctx, "org_a", []ProviderCredential{{KeyID: "key_1", Provider: "anthropic", Value: "sk-one"}}); err != nil {
		t.Fatal(err)
	}
	if len(reg.replaced) != 1 || !slices.Equal(reg.replaced[0], []string{"org_orga_anthropic_"}) {
		t.Fatalf("replace prefixes %v", reg.replaced)
	}
	if len(reg.released) != 1 || !slices.Equal(reg.released[0], []string{"org_orga_openai_"}) {
		t.Fatalf("dropped provider releases %v", reg.released)
	}
	if _, ok := st.byOrg["org_a"]["openai"]; ok {
		t.Fatal("deleted provider still in the gateway's state")
	}
	if g.orgSlotKeyID(OrgSlotName("org_a", "openai", 1)) != "" {
		t.Fatal("deleted provider's key ids kept")
	}
	if err := g.ReleaseOrgCredentials(ctx, "org_a"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reg.released[len(reg.released)-1], []string{"org_orga_"}) {
		t.Fatalf("release prefixes %v", reg.released)
	}
	if len(st.byOrg) != 0 || len(st.expOrg) != 0 || g.orgSlotKeyID(OrgSlotName("org_a", "anthropic", 1)) != "" {
		t.Fatalf("state kept: %v %v", st.byOrg, st.expOrg)
	}
	if err := g.ReleaseOrgCredentials(ctx, "org_a"); err != nil {
		t.Fatalf("second release: %v", err)
	}
}
