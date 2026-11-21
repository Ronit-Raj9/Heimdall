package handlers

// Per-key prices (lane X05, N2): a provider key may carry what the org pays
// per million tokens. Runs of a model the pinned price snapshot does not
// list (a custom endpoint such as xcodebest) were sealed with an unknown
// cost; the report card and the agent leaderboard cost them from the key's
// price at read time, never by changing the sealed run.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// KeyLister lists an org's keys (keystore.Store satisfies it).
type KeyLister interface {
	List(ctx context.Context, orgID string, k keystore.Kind) ([]keystore.Key, error)
}

// KeyPriceLookup returns the price of a model from the org's priced
// provider keys, or nil when no key has a price (or keys is nil). A key
// prices a model it lists (with or without a "<provider>/" prefix) or any
// "<provider>/<model>" of its own provider when it lists no model. Keys are
// tried in the store's order.
func KeyPriceLookup(ctx context.Context, keys KeyLister, orgID string) func(model string) (evidence.ModelPrice, bool) {
	if keys == nil {
		return nil
	}
	list, err := keys.List(ctx, orgID, keystore.KindProvider)
	if err != nil {
		return nil
	}
	var priced []keystore.Key
	for _, k := range list {
		if k.Price != nil && (k.Kind == "" || k.Kind == keystore.KindProvider) {
			priced = append(priced, k)
		}
	}
	if len(priced) == 0 {
		return nil
	}
	return func(model string) (evidence.ModelPrice, bool) {
		model = strings.TrimSpace(model)
		if model == "" {
			return evidence.ModelPrice{}, false
		}
		prefix, bare, _ := strings.Cut(model, "/")
		if !strings.Contains(model, "/") {
			prefix, bare = "", model
		}
		for _, k := range priced {
			if keyPrices(k, model, prefix, bare) {
				p := *k.Price
				return evidence.ModelPrice{InputPerMTok: p.InputPerMTok, OutputPerMTok: p.OutputPerMTok,
					CacheReadPerMTok: p.CacheRate(), CacheWritePerMTok: p.CacheRate()}, true
			}
		}
		return evidence.ModelPrice{}, false
	}
}

// keyPrices reports whether key k prices model (split into its provider
// prefix, "" without one, and the bare model name).
func keyPrices(k keystore.Key, model, prefix, bare string) bool {
	if prefix != "" && prefix != k.Provider {
		return false
	}
	if len(k.Models) == 0 {
		return prefix != "" // a key with no model list prices its provider's "<provider>/<model>" only
	}
	for _, m := range k.Models {
		if m == model || m == bare || strings.TrimPrefix(m, k.Provider+"/") == bare {
			return true
		}
	}
	return false
}

// pricedSource is a leaderboard source whose metrics.json reads carry a
// cost priced from the org's key prices where the run's sealed cost was
// unknown only for want of a price. Every other read, the seal check
// included, is the underlying source's.
type pricedSource struct {
	leaderboardSource
	lookup func(model string) (evidence.ModelPrice, bool)
}

// PricedLeaderboardSource is LeaderboardSource with key prices applied, or
// the plain source when lookup is nil.
func PricedLeaderboardSource(index ReadIndex, lookup func(model string) (evidence.ModelPrice, bool)) leaderboard.Source {
	if lookup == nil {
		return leaderboardSource{index: index}
	}
	return pricedSource{leaderboardSource: leaderboardSource{index: index}, lookup: lookup}
}

// RunFile implements leaderboard.Source.
func (s pricedSource) RunFile(runID, name string) ([]byte, error) {
	data, err := s.leaderboardSource.RunFile(runID, name)
	if err != nil || name != "metrics.json" {
		return data, err
	}
	var metrics map[string]schemas.Value
	if json.Unmarshal(data, &metrics) != nil {
		return data, nil
	}
	cost, ok := metrics["cost_usd"]
	// Only a cost unknown for unpriced rows (metrics/costusd.go): an
	// incomplete gateway log stays unknown whatever the price.
	if !ok || cost.State != schemas.ValueUnknown || !strings.Contains(cost.Reason, "cost rows unpriced") {
		return data, nil
	}
	raw, err := s.leaderboardSource.RunFile(runID, "cost.json")
	if err != nil {
		return data, nil
	}
	var rows []schemas.CostRow
	if json.Unmarshal(raw, &rows) != nil {
		return data, nil
	}
	v, ok := evidence.RepriceCost(rows, s.lookup)
	if !ok {
		return data, nil
	}
	metrics["cost_usd"] = v
	out, err := json.Marshal(metrics)
	if err != nil {
		return data, nil
	}
	return out, nil
}
