package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
)

// The run summary says whether spent_usd is simulated or modeled, so a
// scripted run's priced usage is not read as provider spend (audit 5-A-05).
func TestSpendNoteNamesTheBasis(t *testing.T) {
	sim := spendNote(&engine.Summary{SpentUSD: 0.008772, SpendBasis: engine.SpendSimulated})
	if !strings.Contains(sim, "0.0088 USD simulated") || !strings.Contains(sim, "no provider was charged") {
		t.Fatalf("simulated note %q", sim)
	}
	mod := spendNote(&engine.Summary{SpentUSD: 1.5, SpendBasis: engine.SpendModeled})
	if !strings.Contains(mod, "1.5000 USD modeled") || !strings.Contains(mod, "not a provider bill") {
		t.Fatalf("modeled note %q", mod)
	}
}
