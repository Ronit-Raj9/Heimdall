package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// G10 model key budgets. A provider key over its monthly budget is never
// delivered to a runner (lease credentials, local delivery), and the planner
// and QuickRun refuse a run whose provider has keys but none under budget.

// overBudget reports whether k has a monthly budget and spent all of it.
func overBudget(k keystore.Key) bool {
	return k.BudgetUSD > 0 && k.MonthSpentUSD >= k.BudgetUSD
}

// keyName is how a key is named in a message: its label, else its id.
func keyName(k keystore.Key) string {
	if l := strings.TrimSpace(k.Label); l != "" {
		return fmt.Sprintf("%q", l)
	}
	return k.ID
}

// ErrKeyOverBudget marks the refusal of a run that only over-budget keys
// could serve (HTTP 409).
var ErrKeyOverBudget = errors.New("model key over budget")

type overBudgetError struct{ msg string }

func (e *overBudgetError) Error() string { return e.msg }
func (e *overBudgetError) Unwrap() error { return ErrKeyOverBudget }

func errKeyOverBudget(k keystore.Key) error {
	return &overBudgetError{msg: fmt.Sprintf("key %s is over its budget; raise it on Models (spent $%.2f of $%.2f this month)", keyName(k), k.MonthSpentUSD, k.BudgetUSD)}
}

// sameProvider matches a key's provider to a run's gateway provider, with
// the gateway's aliases (openai-compatible keys serve the openai provider).
func sameProvider(keyProvider, runProvider string) bool {
	norm := func(p string) string {
		switch p = strings.ToLower(strings.TrimSpace(p)); p {
		case "openai-compatible":
			return "openai"
		case "google":
			return "gemini"
		}
		return p
	}
	return norm(keyProvider) == norm(runProvider)
}

// UsableProviderCredentials reveals every provider key of the org that may
// serve a run: usable status and under budget. A key that cannot be
// decrypted is skipped, never echoed.
func UsableProviderCredentials(ctx context.Context, keys KeySource, orgID string) ([]schemas.ProviderCredential, error) {
	list, err := keys.List(ctx, orgID, keystore.KindProvider)
	if err != nil {
		return nil, err
	}
	out := []schemas.ProviderCredential{}
	for _, k := range list {
		if k.Kind != "" && k.Kind != keystore.KindProvider || !usableKey(k) || k.Provider == "" {
			continue
		}
		v, err := keys.Reveal(ctx, orgID, k.ID)
		if err != nil || v == "" {
			continue
		}
		out = append(out, schemas.ProviderCredential{KeyID: k.ID, Provider: k.Provider, BaseURL: k.BaseURL, Models: k.Models, Value: v})
	}
	return out, nil
}

// KeyBudgetRefusal returns the refusal for a run on providers when the org
// has keys for one of them but every such key is over its budget. A key id
// the run pinned (QuickRun's keyId) is checked on its own. A provider the
// org has no key for is not refused: the runner's own keys serve it.
func KeyBudgetRefusal(ctx context.Context, keys KeySource, orgID string, providers, keyIDs []string) error {
	if keys == nil {
		return nil
	}
	list, err := keys.List(ctx, orgID, keystore.KindProvider)
	if err != nil {
		return nil // the key store being down never blocks a run; delivery skips the keys
	}
	for _, id := range keyIDs {
		for _, k := range list {
			if id != "" && k.ID == id && overBudget(k) {
				return errKeyOverBudget(k)
			}
		}
	}
	for _, p := range providers {
		if strings.TrimSpace(p) == "" {
			continue
		}
		var over *keystore.Key
		usable := false
		for i, k := range list {
			if !sameProvider(k.Provider, p) {
				continue
			}
			if usableKey(k) {
				usable = true
				break
			}
			if overBudget(k) && over == nil {
				over = &list[i]
			}
		}
		if !usable && over != nil {
			return errKeyOverBudget(*over)
		}
	}
	return nil
}

// ManifestProviders are the gateway providers a manifest's arms name.
func ManifestProviders(m *schemas.Manifest) []string {
	if m == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(a *schemas.ArmManifest) {
		if a == nil {
			return
		}
		if p := a.Config.Gateway.Provider; p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, a := range m.Arms {
		add(&a)
	}
	add(m.Compatibility)
	return out
}
