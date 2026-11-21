package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// G10 model key budgets and local key delivery.
//
//   - Budgets: after each sealed run the launcher charges the run's cost to
//     the organization key that served it (run.json gateway.org_key_id, set
//     when the run used organization key slots). An unknown cost is charged
//     at budget.reserve_usd (engine.DefaultUnknownRunUSD without one). A key
//     over its monthly budget is not delivered to runners, and Plan and
//     Start refuse a run whose provider has only over-budget keys.
//   - Local delivery: a local serve applies the organization's keys from
//     the key store to its own gateway before a run, so a key added on the
//     Models page works with no environment variables.

// keyBudgetStore is the slice of the key store the launcher needs.
type keyBudgetStore interface {
	handlers.KeySource
	AddSpend(ctx context.Context, orgID, id string, usd float64) error
}

// launcherKeys returns the launcher's key store, nil when none is ready.
// Tests replace it.
var launcherKeys = func(l *launcher) keyBudgetStore {
	if l == nil || l.a == nil || l.a.cfg == nil {
		return nil
	}
	s, ok := sharedKeyStore(l.a).(*lazyKeyStore)
	if !ok || !s.ready() {
		return nil
	}
	return s
}

// ready reports whether the key store is open or can open without creating
// a master key: a server that never stored a key never opens the database
// or writes ~/.bifrost-bench/master.key because of a run.
func (l *lazyKeyStore) ready() bool {
	l.mu.Lock()
	open := l.store != nil
	l.mu.Unlock()
	if open || strings.TrimSpace(os.Getenv(keystore.MasterKeyEnv)) != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".bifrost-bench", "master.key"))
	return err == nil
}

// keyBudgetCheck refuses a manifest whose provider has organization keys
// but every one of them is over its budget.
func (l *launcher) keyBudgetCheck(ctx context.Context, m *schemas.Manifest) error {
	keys := launcherKeys(l)
	if keys == nil || m == nil {
		return nil
	}
	return handlers.KeyBudgetRefusal(ctx, keys, l.org, handlers.ManifestProviders(m), nil)
}

// applyLocalOrgKeys hands the organization's usable keys to this process's
// gateway and binds the experiment to them. Hosted launchers run nothing
// locally (runners fetch keys per lease), so they skip it. Any failure
// leaves the gateway's own keys in place and is logged.
func (l *launcher) applyLocalOrgKeys(ctx context.Context, m *schemas.Manifest, log jobs.Logger) {
	if l.remoteOnly || m == nil || l.a == nil || l.a.reg == nil || l.a.reg.Gateway == nil {
		return
	}
	gw, ok := l.a.reg.Gateway.(engine.OrgCredentialGateway)
	if !ok {
		return
	}
	keys := launcherKeys(l)
	if keys == nil {
		return
	}
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	creds, err := handlers.UsableProviderCredentials(work, keys, l.org)
	if err != nil {
		log.Warnf("model keys: could not read the key store (%v); using the gateway's own provider keys", err)
		return
	}
	if len(creds) == 0 {
		return
	}
	if err := gw.ApplyOrgCredentials(work, l.org, creds); err != nil {
		log.Warnf("model keys: %v; using the gateway's own provider keys", err)
		return
	}
	gw.BindExperimentOrg(m.ExpID, l.org)
	log.Printf("model keys: %d key(s) from the Models page serve this experiment", len(creds))
}

// chargeKeySpend adds a sealed run's cost to the organization key that
// served it. A run on the gateway's own keys charges nothing.
func (l *launcher) chargeKeySpend(ctx context.Context, m *schemas.Manifest, runID string, log jobs.Logger) {
	if m == nil || runID == "" || l.a == nil || l.a.store == nil {
		return
	}
	keys := launcherKeys(l)
	if keys == nil {
		return
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	runs, err := l.a.store.SealedRuns(work, m.ExpID, schemas.GradeSourceOriginal)
	if err != nil {
		log.Warnf("model key budget: read run %s: %v", runID, err)
		return
	}
	for _, sr := range runs {
		if sr.Run.RunID != runID {
			continue
		}
		keyID := sr.Run.Gateway.OrgKeyID
		if keyID == "" {
			return
		}
		run := sr.Run
		usd := runKeyCharge(sr.Dir, sr.Metrics, &run, m)
		if usd <= 0 {
			return
		}
		if err := keys.AddSpend(work, l.org, keyID, usd); err != nil {
			log.Warnf("model key budget: charge $%.4f to key %s: %v", usd, keyID, err)
		}
		return
	}
}

// runKeyCharge is what a sealed run costs its key: the observed spend, plus
// budget.reserve_usd (or engine.DefaultUnknownRunUSD) when part of it was
// not observed (an unpriced model).
func runKeyCharge(dir string, metrics map[string]schemas.Value, run *schemas.Run, m *schemas.Manifest) float64 {
	c, known := engine.RunSpendUSD(dir, metrics, run)
	if !known {
		est := engine.DefaultUnknownRunUSD
		if m != nil && m.Experiment.Budget.ReserveUSD > 0 {
			est = m.Experiment.Budget.ReserveUSD
		}
		c += est
	}
	return c
}
