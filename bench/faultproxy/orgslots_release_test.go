package faultproxy

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func orgSlotsN(org string, from, to int, key string) []OrgSlot {
	var out []OrgSlot
	for i := from; i <= to; i++ {
		n := fmt.Sprintf("org_%s_anthropic_%d", org, i)
		out = append(out, OrgSlot{Name: n, Provider: ProviderAnthropic, Auth: n, Key: key})
	}
	return out
}

func orgCount(env *testEnv) int {
	env.srv.org.mu.RLock()
	defer env.srv.org.mu.RUnlock()
	return len(env.srv.org.byName)
}

// G3 B7: re-registering the same names never counts twice toward the cap.
func TestOrgSlotsCapCountsUniqueNames(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx := context.Background()
	full := orgSlotsN("big", 1, maxOrgSlots-8, "k1")
	for i := 0; i < len(full); i += 500 {
		if err := env.client.RegisterOrgSlots(ctx, full[i:min(i+500, len(full))]); err != nil {
			t.Fatalf("fill: %v", err)
		}
	}
	mine := orgSlotsN("acme", 1, 8, "k1")
	for i := 0; i < 5; i++ { // one registration per lease
		if err := env.client.RegisterOrgSlots(ctx, mine); err != nil {
			t.Fatalf("lease %d: %v", i, err)
		}
	}
	if got := orgCount(env); got != maxOrgSlots {
		t.Fatalf("slots %d, want %d", got, maxOrgSlots)
	}
	if err := env.client.RegisterOrgSlots(ctx, orgSlotsN("new", 1, 1, "k")); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("over the cap: %v", err)
	}
	// Releasing an org frees its slots for the next one.
	if err := env.client.ReleaseOrgSlots(ctx, []string{"org_acme_"}); err != nil {
		t.Fatal(err)
	}
	if err := env.client.RegisterOrgSlots(ctx, orgSlotsN("new", 1, 8, "k")); err != nil {
		t.Fatalf("after release: %v", err)
	}
	if _, ok := env.srv.org.entry("org_acme_anthropic_1"); ok {
		t.Fatal("released slot still registered")
	}
	if _, ok := env.srv.org.lookupAuth("org_acme_anthropic_1"); ok {
		t.Fatal("released placeholder still resolves")
	}
}

// Replace drops the slots of a shrunk key set, but never a claimed one.
func TestOrgSlotsReplaceAndClaims(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx := context.Background()
	if err := env.client.RegisterOrgSlots(ctx, orgSlotsN("acme", 1, 10, "old")); err != nil {
		t.Fatal(err)
	}
	owner := NewClient(env.ctrl.URL, WithToken(env.srv.ControlToken()), WithOwner(strings.Repeat("o", 32)))
	if _, err := owner.ClaimSlots(ctx, ClaimRequest{Slots: []string{"org_acme_anthropic_10"}, RunID: "r_1"}); err != nil {
		t.Fatal(err)
	}
	if err := env.client.ReplaceOrgSlots(ctx, orgSlotsN("acme", 1, 8, "new"), []string{"org_acme_anthropic_"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.srv.org.entry("org_acme_anthropic_9"); ok {
		t.Fatal("slot 9 of the old key set kept")
	}
	if _, ok := env.srv.org.entry("org_acme_anthropic_10"); !ok {
		t.Fatal("claimed slot 10 dropped while a run holds it")
	}
	if e, _ := env.srv.org.entry("org_acme_anthropic_1"); e == nil || e.key != "new" {
		t.Fatal("rotated key not swapped in")
	}
	// Release keeps the claimed slot too.
	if err := env.client.ReleaseOrgSlots(ctx, []string{"org_acme_"}); err != nil {
		t.Fatal(err)
	}
	if got := orgCount(env); got != 1 {
		t.Fatalf("after release %d slots, want the claimed one", got)
	}
	for _, bad := range [][]string{nil, {"org_"}, {"slot-a"}} {
		if err := env.client.ReleaseOrgSlots(ctx, bad); err == nil {
			t.Errorf("release %v accepted", bad)
		}
	}
	if err := env.client.ReplaceOrgSlots(ctx, orgSlotsN("acme", 1, 1, "k"), []string{"x"}); err == nil {
		t.Error("bad replace prefix accepted")
	}
}

// Slots no runner registered and nothing called for orgSlotTTL expire,
// unless claimed.
func TestOrgSlotsExpire(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx := context.Background()
	if err := env.client.RegisterOrgSlots(ctx, append(orgSlotsN("gone", 1, 2, "k"), orgSlotsN("live", 1, 1, "k")...)); err != nil {
		t.Fatal(err)
	}
	owner := NewClient(env.ctrl.URL, WithToken(env.srv.ControlToken()), WithOwner(strings.Repeat("p", 32)))
	if _, err := owner.ClaimSlots(ctx, ClaimRequest{Slots: []string{"org_gone_anthropic_2"}, RunID: "r_2"}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-orgSlotTTL - time.Minute).Unix()
	env.srv.org.mu.Lock()
	env.srv.org.byName["org_gone_anthropic_1"].seen.Store(old)
	env.srv.org.byName["org_gone_anthropic_2"].seen.Store(old)
	env.srv.org.mu.Unlock()
	// A call refreshes a slot.
	env.srv.org.lookupAuth("org_live_anthropic_1")
	if err := env.client.RegisterOrgSlots(ctx, orgSlotsN("other", 1, 1, "k")); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.srv.org.entry("org_gone_anthropic_1"); ok {
		t.Fatal("stale slot kept")
	}
	if _, ok := env.srv.org.entry("org_gone_anthropic_2"); !ok {
		t.Fatal("claimed stale slot dropped")
	}
	if _, ok := env.srv.org.entry("org_live_anthropic_1"); !ok {
		t.Fatal("live slot dropped")
	}
}
