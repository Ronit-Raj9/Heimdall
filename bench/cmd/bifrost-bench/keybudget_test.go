package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeOrgGateway records the organization keys a local launcher applies.
type fakeOrgGateway struct {
	schemas.Gateway
	org   string
	creds []schemas.ProviderCredential
	bound map[string]string
}

func (f *fakeOrgGateway) ApplyOrgCredentials(_ context.Context, orgID string, creds []schemas.ProviderCredential) error {
	f.org, f.creds = orgID, creds
	return nil
}

func (f *fakeOrgGateway) BindExperimentOrg(expID, orgID string) { f.bound[expID] = orgID }

// memKeys is a keystore.Store as the launcher's key store.
func withLauncherKeys(t *testing.T, ks keystore.Store) {
	t.Helper()
	prev := launcherKeys
	launcherKeys = func(*launcher) keyBudgetStore { return ks }
	t.Cleanup(func() { launcherKeys = prev })
}

// TestLocalLauncherDeliversKeys checks G10 local delivery: a local launcher
// hands the Models page keys under budget to its gateway and binds the
// experiment; a hosted (remote only) launcher never does; a provider with
// only over-budget keys is refused at plan time.
func TestLocalLauncherDeliversKeys(t *testing.T) {
	ctx := context.Background()
	ks := keystore.NewMemory()
	put := func(provider, label string, budget, spent float64) *keystore.Key {
		k, err := ks.Create(ctx, "local", keystore.KindProvider, keystore.Put{Provider: provider, Label: label, Value: "sk-" + label + "-123456789", BudgetUSD: budget}, "t")
		if err != nil {
			t.Fatal(err)
		}
		if spent > 0 {
			_ = ks.AddSpend(ctx, "local", k.ID, spent)
		}
		return k
	}
	a := put("anthropic", "a1", 0, 0)
	b := put("anthropic", "a2", 5, 1)
	put("openai", "spent", 2, 3)
	withLauncherKeys(t, ks)

	gw := &fakeOrgGateway{bound: map[string]string{}}
	l := &launcher{a: &app{reg: &schemas.Registry{Gateway: gw}}, org: "local"}
	m := &schemas.Manifest{ExpID: "exp_1", Arms: map[string]schemas.ArmManifest{}}
	l.applyLocalOrgKeys(ctx, m, discardLog{})
	if gw.org != "local" || len(gw.creds) != 2 || gw.bound["exp_1"] != "local" {
		t.Fatalf("delivered %+v bound %v", gw.creds, gw.bound)
	}
	ids := map[string]bool{gw.creds[0].KeyID: true, gw.creds[1].KeyID: true}
	if !ids[a.ID] || !ids[b.ID] {
		t.Fatalf("delivered ids %v", ids)
	}

	hosted := &fakeOrgGateway{bound: map[string]string{}}
	hl := &launcher{a: &app{reg: &schemas.Registry{Gateway: hosted}}, org: "local", remoteOnly: true}
	hl.applyLocalOrgKeys(ctx, m, discardLog{})
	if hosted.creds != nil {
		t.Fatal("a hosted launcher applied keys in the server process")
	}

	arm := schemas.ArmManifest{}
	arm.Config.Gateway.Provider = "openai"
	m.Arms["A"] = arm
	if err := l.keyBudgetCheck(ctx, m); err == nil {
		t.Fatal("a provider with only an over-budget key was planned")
	}
	arm.Config.Gateway.Provider = "anthropic"
	m.Arms["A"] = arm
	if err := l.keyBudgetCheck(ctx, m); err != nil {
		t.Fatalf("usable provider refused: %v", err)
	}
}

// TestRunKeyCharge checks an unpriced run is charged budget.reserve_usd.
func TestRunKeyCharge(t *testing.T) {
	now := time.Now()
	run := &schemas.Run{RunID: "r_a"}
	run.Timing.AgentStarted = &now
	m := &schemas.Manifest{}
	m.Experiment.Budget.ReserveUSD = 0.75
	if got := runKeyCharge("", nil, run, m); got != 0.75 {
		t.Fatalf("unknown cost charged %v, want reserve 0.75", got)
	}
	if got := runKeyCharge("", nil, &schemas.Run{RunID: "r_b"}, m); got != 0 {
		t.Fatalf("a run whose agent never started charged %v", got)
	}
}

// TestHostedLauncherRequiresOrgKeys covers X01: a hosted (remote only)
// launcher refuses a provider the org has no usable key for, refuses
// everything when no key store is ready, and lets a smoke experiment
// (scripted model only) through.
func TestHostedLauncherRequiresOrgKeys(t *testing.T) {
	ctx := context.Background()
	ks := keystore.NewMemory()
	if _, err := ks.Create(ctx, "org_h", keystore.KindProvider, keystore.Put{Provider: "anthropic", Label: "main", Value: "sk-main-123456789"}, "t"); err != nil {
		t.Fatal(err)
	}
	withLauncherKeys(t, ks)
	l := &launcher{a: &app{reg: &schemas.Registry{}}, org: "org_h", remoteOnly: true}
	m := &schemas.Manifest{ExpID: "exp_h", Arms: map[string]schemas.ArmManifest{}}
	arm := schemas.ArmManifest{}
	arm.Config.Adapter = "command"
	arm.Config.Gateway.Provider = "openai"
	m.Arms["A"] = arm
	if err := l.keyBudgetCheck(ctx, m); !errors.Is(err, handlers.ErrModelKeyMissing) {
		t.Fatalf("hosted launcher with no openai key: %v", err)
	}
	local := &launcher{a: &app{reg: &schemas.Registry{}}, org: "org_h"}
	if err := local.keyBudgetCheck(ctx, m); err != nil {
		t.Fatalf("a local launcher must not require a key: %v", err)
	}
	arm.Config.Gateway.Provider = "anthropic"
	m.Arms["A"] = arm
	if err := l.keyBudgetCheck(ctx, m); err != nil {
		t.Fatalf("usable key refused: %v", err)
	}
	launcherKeys = func(*launcher) keyBudgetStore { return nil }
	if err := l.keyBudgetCheck(ctx, m); !errors.Is(err, handlers.ErrKeyStoreUnavailable) {
		t.Fatalf("hosted with no key store: %v", err)
	}
	m.Experiment.Smoke = true
	if err := l.keyBudgetCheck(ctx, m); err != nil {
		t.Fatalf("smoke experiment refused: %v", err)
	}
}
