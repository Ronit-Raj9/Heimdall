package handlers

// Per-key prices (lane X05, N2): a provider key may carry what the org pays
// per million tokens. Runs of a model the pinned price snapshot does not
// list (a custom endpoint such as xcodebest) were sealed with an unknown
// cost; the report card, the agent leaderboard and usage cost them from the
// key's price (else the server's price list, pricelist.go) at read time,
// never by changing the sealed run.

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

// KeyPriceLookup returns the price of a model for an org: its priced
// provider keys first, then the server's price list (SetPriceList). It is
// nil when no key has a price and no price list is installed. See
// resolvePrice for the order. Keys are tried in the store's order; a key
// store that cannot be listed counts as no keys.
func KeyPriceLookup(ctx context.Context, keys KeyLister, orgID string) func(model string) (evidence.ModelPrice, bool) {
	var list []keystore.Key
	if keys != nil {
		if l, err := keys.List(ctx, orgID, keystore.KindProvider); err == nil {
			list = l
		}
	}
	return priceLookup(list)
}

// priceLookup is KeyPriceLookup over an already listed set of keys.
func priceLookup(list []keystore.Key) func(model string) (evidence.ModelPrice, bool) {
	priced := pricedKeys(list)
	if len(priced) == 0 && !hasPriceList() {
		return nil
	}
	return func(model string) (evidence.ModelPrice, bool) {
		r, ok := resolvePrice(priced, model)
		return r.price, ok
	}
}

// pricedKeys keeps the provider keys that carry a price.
func pricedKeys(list []keystore.Key) []keystore.Key {
	var priced []keystore.Key
	for _, k := range list {
		if k.Price != nil && (k.Kind == "" || k.Kind == keystore.KindProvider) {
			priced = append(priced, k)
		}
	}
	return priced
}

// Where a resolved price comes from.
const (
	PriceSourceKey       = "key"
	PriceSourcePriceList = "price_list"
)

// resolvedPrice is a model's price and where it was found.
type resolvedPrice struct {
	price    evidence.ModelPrice
	source   string // PriceSourceKey or PriceSourcePriceList
	keyID    string // the key, for PriceSourceKey
	keyLabel string
}

// resolvePrice finds the price of model in this order: (a) a priced key
// that lists the model (with or without a "<provider>/" prefix), or that
// lists no model and is of the provider a "<provider>/<model>" name names;
// (b) for a bare model name (no "/"), the priced key with an empty model
// list when exactly one such key exists; (c) the server's price list.
func resolvePrice(priced []keystore.Key, model string) (resolvedPrice, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return resolvedPrice{}, false
	}
	prefix, bare, _ := strings.Cut(model, "/")
	if !strings.Contains(model, "/") {
		prefix, bare = "", model
	}
	fromKey := func(k keystore.Key) resolvedPrice {
		return resolvedPrice{price: keyModelPrice(*k.Price), source: PriceSourceKey, keyID: k.ID, keyLabel: k.Label}
	}
	for _, k := range priced {
		if keyPrices(k, model, prefix, bare) {
			return fromKey(k), true
		}
	}
	if prefix == "" {
		var open []keystore.Key
		for _, k := range priced {
			if len(k.Models) == 0 {
				open = append(open, k)
			}
		}
		if len(open) == 1 {
			return fromKey(open[0]), true
		}
	}
	if p, ok := listPrice(model); ok {
		return resolvedPrice{price: p, source: PriceSourcePriceList}, true
	}
	return resolvedPrice{}, false
}

// keyModelPrice is a key's price as a price table entry.
func keyModelPrice(p keystore.Price) evidence.ModelPrice {
	return evidence.ModelPrice{InputPerMTok: p.InputPerMTok, OutputPerMTok: p.OutputPerMTok,
		CacheReadPerMTok: p.CacheRate(), CacheWritePerMTok: p.CacheRate()}
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
	v, ok := repricedCost(metrics, func() ([]byte, error) { return s.leaderboardSource.RunFile(runID, FileCost) }, s.lookup)
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

// repricedCost is the cost_usd of a run's metrics priced with lookup, when
// the sealed cost is unknown only for want of a price (metrics/costusd.go)
// and every unpriced cost row (read by readCost) now has a price. An
// incomplete gateway log stays unknown whatever the price.
func repricedCost(metrics map[string]schemas.Value, readCost func() ([]byte, error), lookup func(model string) (evidence.ModelPrice, bool)) (schemas.Value, bool) {
	cost, ok := metrics[costMetric]
	if lookup == nil || !ok || cost.State != schemas.ValueUnknown || !strings.Contains(cost.Reason, "cost rows unpriced") {
		return schemas.Value{}, false
	}
	raw, err := readCost()
	if err != nil {
		return schemas.Value{}, false
	}
	var rows []schemas.CostRow
	if json.Unmarshal(raw, &rows) != nil {
		return schemas.Value{}, false
	}
	return evidence.RepriceCost(rows, lookup)
}
