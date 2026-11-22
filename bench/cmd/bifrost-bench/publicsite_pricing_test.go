package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/billing"
)

func TestSitePricing(t *testing.T) {
	// Billing off (no service): the beta page.
	if p := sitePricing(nil)(); !p.Beta {
		t.Fatalf("billing off: %+v", p)
	}
	p := pricingFromBilling((*billing.Service)(nil).Pricing())
	if p.Beta || len(p.Plans) != len(billing.Plans) {
		t.Fatalf("plans: %+v", p)
	}
	team := p.Plans[1]
	if team.Price == "" || team.Period != "per seat per month" || len(team.Features) == 0 || len(team.Limits) != 3 {
		t.Fatalf("team: %+v", team)
	}
	if ent := p.Plans[2]; ent.CTA != "contact" {
		t.Fatalf("enterprise: %+v", ent)
	}
}
