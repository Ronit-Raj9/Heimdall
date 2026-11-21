package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
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

// ModelKeysLink is the app page where an organization adds its model keys.
const ModelKeysLink = "/models"

// ErrModelKeyMissing marks the refusal of a run on a provider the
// organization has no usable key for (X01; HTTP 409 with ModelKeysLink).
var ErrModelKeyMissing = errors.New("no model key for the provider")

// ErrKeyStoreUnavailable marks a refusal because the key store could not be
// read: a run is never started on keys nobody checked (HTTP 503).
var ErrKeyStoreUnavailable = errors.New("model key store unavailable")

type missingKeyError struct{ provider string }

func (e *missingKeyError) Error() string {
	return fmt.Sprintf("this organization has no working %s key: add one on Models & keys, then start again (runs use only the organization's own model keys)", e.provider)
}
func (e *missingKeyError) Unwrap() error { return ErrModelKeyMissing }

type keyStoreError struct{ err error }

func (e *keyStoreError) Error() string {
	return "could not read the organization's model keys, so nothing was started; try again in a minute (" + e.err.Error() + ")"
}
func (e *keyStoreError) Unwrap() error { return ErrKeyStoreUnavailable }

// KeyBudgetRefusal returns the refusal for a run on providers when the org
// has keys for one of them but every such key is over its budget, or when
// the key store cannot be read (it fails closed). A provider the org has no
// key for is not refused here; ModelKeyRefusal with require refuses it.
func KeyBudgetRefusal(ctx context.Context, keys KeySource, orgID string, providers, keyIDs []string) error {
	return ModelKeyRefusal(ctx, keys, orgID, providers, keyIDs, false)
}

// ModelKeyRefusal is KeyBudgetRefusal plus, with require (hosted orgs, X01),
// a refusal for every provider the org has no usable key for: org runs never
// fall back to the server's or a runner's own provider keys. A key id the
// run pinned (QuickRun's keyId) is checked on its own. With require and no
// key store at all, every provider is refused as unavailable.
func ModelKeyRefusal(ctx context.Context, keys KeySource, orgID string, providers, keyIDs []string, require bool) error {
	want := []string{}
	for _, p := range providers {
		if p = strings.TrimSpace(p); p != "" {
			want = append(want, p)
		}
	}
	if keys == nil {
		if require && len(want) > 0 {
			return &keyStoreError{err: errors.New("model keys are not configured on this server")}
		}
		return nil
	}
	if len(want) == 0 && len(keyIDs) == 0 {
		return nil
	}
	list, err := keys.List(ctx, orgID, keystore.KindProvider)
	if err != nil {
		return &keyStoreError{err: err} // fail closed: never start a run on keys nobody checked
	}
	for _, id := range keyIDs {
		for _, k := range list {
			if id != "" && k.ID == id && overBudget(k) {
				return errKeyOverBudget(k)
			}
		}
	}
	for _, p := range want {
		var over *keystore.Key
		usable := false
		for i, k := range list {
			if (k.Kind != "" && k.Kind != keystore.KindProvider) || !sameProvider(k.Provider, p) {
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
		switch {
		case usable:
		case over != nil:
			return errKeyOverBudget(*over)
		case require:
			return &missingKeyError{provider: p}
		}
	}
	return nil
}

// SendModelKeyRefusal answers a ModelKeyRefusal error: 409 with a link to
// Models & keys for a missing or over-budget key, 503 when the key store is
// down. It reports false for any other error.
func SendModelKeyRefusal(ctx *fasthttp.RequestCtx, mode schemas.Mode, err error) bool {
	switch {
	case errors.Is(err, ErrModelKeyMissing), errors.Is(err, ErrKeyOverBudget):
		lib.SendErrorLink(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error(), ModelKeysLink)
	case errors.Is(err, ErrKeyStoreUnavailable):
		lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, err.Error())
	default:
		return false
	}
	return true
}

// PaidManifestProviders are the gateway providers of the manifest's arms
// that can spend money on a provider: none for a smoke or scripted_upstream
// experiment (the scripted model answers), and none for a scripted control
// that makes no model call or a deployed agent whose model traffic is
// external (X01; the gateway applies the same rule at mint time).
func PaidManifestProviders(m *schemas.Manifest) []string {
	if m == nil || m.Experiment.NoPaidTraffic() {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(a *schemas.ArmManifest) {
		if a == nil || !armSpends(&a.Config) {
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

// armSpends reports whether an agent config's runs call a model through the
// gateway (see PaidManifestProviders).
func armSpends(c *schemas.AgentConfig) bool {
	if len(c.Team) > 0 {
		return true
	}
	if c.Remote != nil && c.Remote.ModelTraffic != "gateway" {
		return false
	}
	if c.Adapter == "scripted" && c.Scripted != nil && c.Scripted.Kind != "toy" && c.Scripted.Kind != "mcploop" {
		return false
	}
	return true
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

// ErrDailyCap marks a refusal because the organization reached a daily cap
// set by a server admin (quota max_runs_per_day or max_spend_usd_per_day).
var ErrDailyCap = errors.New("organization daily cap reached")

type dailyCapError struct{ msg string }

func (e *dailyCapError) Error() string { return e.msg }
func (e *dailyCapError) Unwrap() error { return ErrDailyCap }

// DailyCapRefusal returns the refusal for new runs when today's sealed runs
// or spend (UTC day) reached the org's caps; a cap of 0 is off. Off by
// default (quota.DefaultLimits).
func DailyCapRefusal(maxRuns int, maxSpendUSD float64, runsToday int, spentToday float64) error {
	switch {
	case maxRuns > 0 && runsToday >= maxRuns:
		return &dailyCapError{msg: fmt.Sprintf("the organization ran %d of its %d runs allowed today (UTC); new runs start tomorrow, or ask your server owner to raise the cap", runsToday, maxRuns)}
	case maxSpendUSD > 0 && spentToday >= maxSpendUSD:
		return &dailyCapError{msg: fmt.Sprintf("the organization spent $%.2f of its $%.2f daily cap today (UTC); new runs start tomorrow, or ask your server owner to raise the cap", spentToday, maxSpendUSD)}
	}
	return nil
}

// UsageToday is the org's sealed runs and their known spend on now's UTC day.
func UsageToday(index ReadIndex, now time.Time) (runs int, spentUSD float64) {
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	rep := ComputeUsage(index, nil, UsageQuery{From: day, To: day, GroupBy: UsageByModel})
	return rep.Runs, rep.TotalCostUSD
}
