package handlers

// Server price list (F1.price): the price snapshot the server loads at start
// (deploy/pricing-snapshot.json) is the last place a model's price is looked
// up, after the org's own key prices (keyprices.go). Readers that cost runs
// at read time (usage, report card, leaderboard) and the default model's
// price hint use it through KeyPriceLookup.

import (
	"sync/atomic"

	"github.com/maximhq/bifrost/bench/evidence"
)

// priceListFunc is the type stored in priceList.
type priceListFunc func(model string) (evidence.ModelPrice, bool)

// priceList holds the server's price list lookup; empty until SetPriceList.
var priceList atomic.Value // priceListFunc

// SetPriceList installs the server's price list (the loaded snapshot's
// Lookup). nil removes it.
func SetPriceList(f func(model string) (evidence.ModelPrice, bool)) {
	priceList.Store(priceListFunc(f))
}

// listPrice returns the price list's price of model, false without a list.
func listPrice(model string) (evidence.ModelPrice, bool) {
	f, _ := priceList.Load().(priceListFunc)
	if f == nil || model == "" {
		return evidence.ModelPrice{}, false
	}
	return f(model)
}

// hasPriceList reports whether a price list is installed.
func hasPriceList() bool {
	f, _ := priceList.Load().(priceListFunc)
	return f != nil
}
