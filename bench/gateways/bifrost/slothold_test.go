package bifrost

import (
	"context"
	"testing"

	"time"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestRevokeKeyHoldSlotsKeepsClaimUntilPlanCleared is the live-11 race: a
// plain RevokeKey frees the slot and its claim, another bench process takes
// the slot at once, and the first run's ClearPlan then fails with 409 on the
// other owner's claim (the run was sealed infra_error). With
// RevokeKeyHoldSlots the slot and its claim stay held until
// ReleaseKeySlots, so the clear succeeds and only then can B take it.
func TestRevokeKeyHoldSlotsKeepsClaimUntilPlanCleared(t *testing.T) {
	fastClaims(t)
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 1)
	ca, cb := proxy.client("host-a pid 1"), proxy.client("host-b pid 2")
	a, _ := claimGateway(t, f, 1, ca, nil)
	b, _ := claimGateway(t, f, 1, cb, nil)
	ctx := context.Background()

	ka, err := a.MintKey(ctx, runContext("r_holda", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.SetPlan(ctx, ka.KeySlot, schemas.FaultPlan{Mode: faultproxy.ModeScripted, RunID: "r_holda"}); err != nil {
		t.Fatal(err)
	}
	var _ schemas.SlotHoldingRevoker = a
	if err := a.RevokeKeyHoldSlots(ctx, ka); err != nil {
		t.Fatal(err)
	}
	if cl, ok := liveClaims(t, ca)[ka.KeySlot]; !ok || cl.OwnerID != ca.OwnerID() {
		t.Fatalf("claim after RevokeKeyHoldSlots = %+v, %v; want still held by A", cl, ok)
	}
	// B cannot take the draining slot, in this process (A) nor in another (B).
	bctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if kb, err := b.MintKey(bctx, runContext("r_holdb", testModel)); err == nil {
		t.Fatalf("B minted %+v on a draining slot", kb)
	}
	actx, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()
	if ka2, err := a.MintKey(actx, runContext("r_holda2", testModel)); err == nil {
		t.Fatalf("A minted %+v on its own draining slot", ka2)
	}
	if err := ca.ClearPlan(ctx, ka.KeySlot); err != nil {
		t.Fatalf("ClearPlan while draining: %v", err)
	}
	a.ReleaseKeySlots(ka)
	a.ReleaseKeySlots(ka) // safe twice
	if _, ok := liveClaims(t, ca)[ka.KeySlot]; ok {
		t.Fatal("ReleaseKeySlots kept the claim")
	}
	kb, err := b.MintKey(ctx, runContext("r_holdb2", testModel))
	if err != nil || kb.KeySlot != ka.KeySlot {
		t.Fatalf("B's mint after release = %+v, %v", kb, err)
	}
}

// TestRevokeKeyStillReleasesAtOnce keeps the plain RevokeKey contract.
func TestRevokeKeyStillReleasesAtOnce(t *testing.T) {
	fastClaims(t)
	f := newFakeBifrost(t)
	proxy := newClaimProxy(t, 1)
	ca := proxy.client("host-a pid 1")
	a, _ := claimGateway(t, f, 1, ca, nil)
	ctx := context.Background()
	ka, err := a.MintKey(ctx, runContext("r_plain", testModel))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RevokeKey(ctx, ka); err != nil {
		t.Fatal(err)
	}
	if _, ok := liveClaims(t, ca)[ka.KeySlot]; ok {
		t.Fatal("RevokeKey kept the claim")
	}
}
