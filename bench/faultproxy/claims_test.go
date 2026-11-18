package faultproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	ownerA = "owner-a-0123456789abcdef"
	ownerB = "owner-b-0123456789abcdef"
)

// clockTable is a plan table on a clock the test moves.
func clockTable() (*planTable, *time.Time) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	pt := newPlanTable()
	pt.now = func() time.Time { return now }
	return pt, &now
}

func TestClaimTableClaimRenewExpiryRelease(t *testing.T) {
	pt, now := clockTable()
	slots := []string{"s1", "s2", "s3"}

	got, exp, held := pt.claim(ownerA, slots, 1, time.Minute, "r_a", "host-a pid 1")
	if len(got) != 1 || got[0] != "s1" || held != nil || !exp.Equal(now.Add(time.Minute)) {
		t.Fatalf("A claim = %v %v %v", got, exp, held)
	}
	// B skips A's slot and takes the next one.
	if got, _, _ := pt.claim(ownerB, slots, 1, time.Minute, "r_b", "host-b pid 2"); len(got) != 1 || got[0] != "s2" {
		t.Fatalf("B claim = %v", got)
	}
	// All or none: B wants two of s1 and s3, gets neither, and sees A's claim.
	got, _, held = pt.claim(ownerB, []string{"s1", "s3"}, 2, time.Minute, "r_b2", "")
	if got != nil || len(held) != 1 || held[0].Slot != "s1" || held[0].OwnerID != OwnerID(ownerA) || held[0].RunID != "r_a" || held[0].Label != "host-a pid 1" {
		t.Fatalf("all-or-none claim = %v, held %+v", got, held)
	}
	if c := pt.liveClaim("s3"); c != nil {
		t.Fatalf("a refused claim left s3 claimed: %+v", c)
	}
	// The owner may claim its own slot again (a new run of the same process).
	if got, _, _ := pt.claim(ownerA, []string{"s1"}, 1, time.Minute, "r_a2", ""); len(got) != 1 {
		t.Fatalf("A re-claim = %v", got)
	}

	// Plan writes: only the owner of a live claim; anyone on an unclaimed slot.
	if held, err := pt.setOwned("s1", ownerB, schemas.FaultPlan{Mode: ModeScripted, RunID: "r_b"}); err != nil || held == nil || held.OwnerID != OwnerID(ownerA) {
		t.Fatalf("B on A's slot: held %+v, err %v", held, err)
	}
	if held, _ := pt.setOwned("s1", "", schemas.FaultPlan{RunID: "r_x"}); held == nil {
		t.Fatal("a write without an owner changed a claimed slot")
	}
	if held, err := pt.setOwned("s1", ownerA, schemas.FaultPlan{Mode: ModeScripted, RunID: "r_a2"}); err != nil || held != nil {
		t.Fatalf("A on its slot: %+v %v", held, err)
	}
	if held, err := pt.setOwned("s3", "", schemas.FaultPlan{RunID: "r_old"}); err != nil || held != nil {
		t.Fatalf("legacy write on an unclaimed slot: %+v %v", held, err)
	}
	if held := pt.clearOwned("s1", ownerB); held == nil {
		t.Fatal("B cleared A's plan")
	}
	if p, ok := pt.get("s1"); !ok || p.RunID != "r_a2" || p.Mode != ModeScripted {
		t.Fatalf("A's plan after B's attempts = %+v %v", p, ok)
	}
	if _, err := pt.setOwned("s1", ownerA, schemas.FaultPlan{Mode: "bad"}); err == nil {
		t.Fatal("an invalid plan was accepted")
	}

	// Renewal keeps A's claim past its first expiry.
	*now = now.Add(50 * time.Second)
	renewed, lost, _ := pt.renew(ownerA, []RenewSlot{{Slot: "s1", RunID: "r_a2"}}, time.Minute, "host-a pid 1")
	if len(renewed) != 1 || lost != nil {
		t.Fatalf("renew = %v lost %v", renewed, lost)
	}
	*now = now.Add(30 * time.Second) // 80s after the claim, 30s after the renewal
	if got, _, _ := pt.claim(ownerB, []string{"s1"}, 1, time.Minute, "r_b3", ""); got != nil {
		t.Fatal("B took a renewed claim")
	}

	// Expiry: A stops renewing (crashed); after the TTL, B may take s1.
	*now = now.Add(31 * time.Second)
	if c := pt.liveClaim("s1"); c != nil {
		t.Fatalf("claim still live after its TTL: %+v", c)
	}
	if got, _, _ := pt.claim(ownerB, []string{"s1"}, 1, time.Minute, "r_b3", ""); len(got) != 1 {
		t.Fatalf("B on an expired claim = %v", got)
	}
	// A comes back and renews: s1 is lost to B, and A's write is refused.
	renewed, lost, _ = pt.renew(ownerA, []RenewSlot{{Slot: "s1", RunID: "r_a2"}}, time.Minute, "")
	if len(renewed) != 0 || len(lost) != 1 || lost[0].OwnerID != OwnerID(ownerB) || lost[0].RunID != "r_b3" {
		t.Fatalf("A renew after loss = %v, lost %+v", renewed, lost)
	}
	if held, _ := pt.setOwned("s1", ownerA, schemas.FaultPlan{RunID: "r_a2"}); held == nil {
		t.Fatal("A replaced B's plan after losing the claim")
	}
	// Renewal re-claims a slot no live claim holds (a proxy restart lost it).
	if renewed, lost, _ := pt.renew(ownerA, []RenewSlot{{Slot: "s3", RunID: "r_a3"}}, time.Minute, ""); len(renewed) != 1 || lost != nil {
		t.Fatalf("renew of a free slot = %v %v", renewed, lost)
	}

	// Release: only the owner's claims, and with a run id only that run's.
	if out := pt.release(ownerA, []string{"s1"}, ""); len(out) != 0 {
		t.Fatalf("A released B's claim: %v", out)
	}
	if out := pt.release(ownerA, []string{"s3"}, "r_other"); len(out) != 0 {
		t.Fatalf("a release for another run dropped the claim: %v", out)
	}
	if out := pt.release(ownerA, []string{"s3"}, "r_a3"); len(out) != 1 {
		t.Fatalf("release = %v", out)
	}
	list := pt.listClaims() // B's s2 claim was never renewed and has expired
	if len(list) != 1 || list[0].Slot != "s1" || list[0].OwnerID != OwnerID(ownerB) {
		t.Fatalf("claims = %+v", list)
	}
}

func TestClaimAPI(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	ctx := context.Background()
	a := NewClient(env.ctrl.URL, WithToken(env.srv.ControlToken()), WithOwnerLabel("host-a pid 1"))
	b := NewClient(env.ctrl.URL, WithToken(env.srv.ControlToken()), WithOwnerLabel("host-b pid 2"))
	if a.OwnerID() == b.OwnerID() || len(a.owner) < minOwnerLen {
		t.Fatalf("clients share an owner or have a short one: %q %q", a.OwnerID(), b.OwnerID())
	}
	h, err := a.Capabilities(ctx)
	if err != nil || !h.SlotClaims {
		t.Fatalf("health = %+v, %v", h, err)
	}

	res, err := a.ClaimSlots(ctx, ClaimRequest{Slots: []string{"slot-a", "slot-b"}, RunID: "r_a", TTLS: 30})
	if err != nil || len(res.Claimed) != 1 || res.Claimed[0] != "slot-a" || res.ExpiresAt.IsZero() {
		t.Fatalf("A claim = %+v, %v", res, err)
	}
	if res, err := b.ClaimSlots(ctx, ClaimRequest{Slots: []string{"slot-a", "slot-b"}, RunID: "r_b"}); err != nil || res.Claimed[0] != "slot-b" {
		t.Fatalf("B claim = %+v, %v", res, err)
	}
	_, err = b.ClaimSlots(ctx, ClaimRequest{Slots: []string{"slot-a"}, RunID: "r_b2"})
	var cc *ClaimConflictError
	if !errors.As(err, &cc) || len(cc.Held) != 1 || cc.Held[0].OwnerID != a.OwnerID() || cc.Held[0].Label != "host-a pid 1" || !IsSlotHeld(err) {
		t.Fatalf("B on A's slot: %v (%+v)", err, cc)
	}

	// A binds a scripted plan; B can neither replace nor clear it.
	scripted := schemas.FaultPlan{Mode: ModeScripted, RunID: "r_a"}
	if err := a.SetPlan(ctx, "slot-a", scripted); err != nil {
		t.Fatal(err)
	}
	err = b.SetPlan(ctx, "slot-a", schemas.FaultPlan{RunID: "r_b"})
	if err == nil || !IsSlotHeld(err) || !strings.Contains(err.Error(), "claimed by another bench process") || !strings.Contains(err.Error(), "run r_a") {
		t.Fatalf("B set on A's slot: %v", err)
	}
	if err := b.ClearPlan(ctx, "slot-a"); err == nil || !IsSlotHeld(err) {
		t.Fatalf("B clear on A's slot: %v", err)
	}
	legacy := NewClient(env.ctrl.URL, WithToken(env.srv.ControlToken()), WithOwner(""))
	if err := legacy.SetPlan(ctx, "slot-a", schemas.FaultPlan{RunID: "r_old"}); err == nil {
		t.Fatal("a client without an owner replaced a claimed slot's plan")
	}
	if p, ok := env.srv.plans.get("slot-a"); !ok || p != scripted {
		t.Fatalf("A's plan = %+v %v", p, ok)
	}
	// The scripted plan still answers A's calls, and the ledger says so.
	env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
	if entries, err := a.Ledger(ctx, "r_a"); err != nil || len(entries) != 1 || entries[0].Upstream != UpstreamScripted {
		t.Fatalf("A's ledger = %+v, %v", entries, err)
	}
	if err := a.ClearPlan(ctx, "slot-a"); err != nil {
		t.Fatalf("owner clear: %v", err)
	}
	// An unclaimed slot still accepts a write without an owner (an older bench).
	if err := a.ReleaseSlots(ctx, ReleaseRequest{Slots: []string{"slot-a"}, RunID: "r_a"}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SetPlan(ctx, "slot-a", schemas.FaultPlan{RunID: "r_old"}); err != nil {
		t.Fatalf("legacy write on an unclaimed slot: %v", err)
	}

	// Renewal and listing; the owner token never shows.
	if rr, err := b.RenewSlots(ctx, RenewRequest{Slots: []RenewSlot{{Slot: "slot-b", RunID: "r_b"}}}); err != nil || len(rr.Renewed) != 1 || len(rr.Lost) != 0 {
		t.Fatalf("renew = %+v, %v", rr, err)
	}
	claims, err := a.Claims(ctx)
	if err != nil || len(claims) != 1 || claims[0].Slot != "slot-b" || claims[0].OwnerID != b.OwnerID() || claims[0].RunID != "r_b" {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	resp, err := http.Get(env.ctrl.URL + "/claims")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), b.owner) {
		t.Fatalf("GET /claims shows an owner token: %s", body)
	}
}

func TestClaimAPIRefusals(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	post := func(path, token string, body any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, env.ctrl.URL+path, bytes.NewReader(b))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	tok := env.srv.ControlToken()
	tests := []struct {
		name  string
		path  string
		token string
		body  any
		want  int
	}{
		{"no token", "/claims", "", ClaimRequest{Owner: ownerA, Slots: []string{"slot-a"}}, http.StatusUnauthorized},
		{"short owner", "/claims", tok, ClaimRequest{Owner: "short", Slots: []string{"slot-a"}}, http.StatusBadRequest},
		{"owner with a space", "/claims", tok, ClaimRequest{Owner: "owner a 0123456789abcdef", Slots: []string{"slot-a"}}, http.StatusBadRequest},
		{"no slots", "/claims", tok, ClaimRequest{Owner: ownerA}, http.StatusBadRequest},
		{"count over offer", "/claims", tok, ClaimRequest{Owner: ownerA, Slots: []string{"slot-a"}, Count: 2}, http.StatusBadRequest},
		{"slot twice", "/claims", tok, ClaimRequest{Owner: ownerA, Slots: []string{"slot-a", "slot-a"}}, http.StatusBadRequest},
		{"unknown slot", "/claims", tok, ClaimRequest{Owner: ownerA, Slots: []string{"nope"}}, http.StatusNotFound},
		{"ttl too long", "/claims", tok, ClaimRequest{Owner: ownerA, Slots: []string{"slot-a"}, TTLS: 3600}, http.StatusBadRequest},
		{"unknown field", "/claims", tok, map[string]any{"owner": ownerA, "slots": []string{"slot-a"}, "steal": true}, http.StatusBadRequest},
		{"renew no token", "/claims/renew", "", RenewRequest{Owner: ownerA, Slots: []RenewSlot{{Slot: "slot-a"}}}, http.StatusUnauthorized},
		{"release no token", "/claims/release", "", ReleaseRequest{Owner: ownerA, Slots: []string{"slot-a"}}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := post(tt.path, tt.token, tt.body); got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
	if claims := env.srv.plans.listClaims(); len(claims) != 0 {
		t.Fatalf("a refused request left claims: %+v", claims)
	}
}

// TestClaimClientOldProxy talks to a proxy older than the claim API: /health
// without slot_claims and no claim routes.
func TestClaimClientOldProxy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","slots":2,"plan_modes":["none","scripted"]}`))
	})
	mux.HandleFunc("PUT /plans/{slot}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	old := httptest.NewServer(mux)
	t.Cleanup(old.Close)
	c := NewClient(old.URL, WithToken("tok"))
	ctx := context.Background()
	h, err := c.Capabilities(ctx)
	if err != nil || h.SlotClaims {
		t.Fatalf("old health = %+v, %v", h, err)
	}
	if _, err := c.ClaimSlots(ctx, ClaimRequest{Slots: []string{"slot-a"}}); !errors.Is(err, ErrClaimsUnsupported) {
		t.Fatalf("claim on an old proxy: %v", err)
	}
	if _, err := c.Claims(ctx); !errors.Is(err, ErrClaimsUnsupported) {
		t.Fatalf("list on an old proxy: %v", err)
	}
	if err := c.SetPlan(ctx, "slot-a", schemas.FaultPlan{RunID: "r_a"}); err != nil {
		t.Fatalf("plan on an old proxy: %v", err)
	}
}
