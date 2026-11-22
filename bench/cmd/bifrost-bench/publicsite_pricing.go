package main

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/billing"
	"github.com/maximhq/bifrost/bench/handlers"
)

// featureWords are the pricing page's words for each billing feature.
var featureWords = map[billing.Feature]string{
	billing.FeaturePrivateBenchmarks: "Your own private benchmarks",
	billing.FeatureCI:                "Pull request gate in CI",
	billing.FeatureAlerts:            "Scheduled checks with alerts and webhooks",
	billing.FeatureOwnWorker:         "Support for running on your own worker",
	billing.FeatureInvoice:           "Pay by invoice",
}

// sitePricing is the public pricing page's plan source (Y08 reads Y16):
// the billing plans while billing is on (HEIMDALL_BILLING=on), else the
// beta page. publicPricing, when a lane sets it, still wins.
func sitePricing(env *hostedEnv) func() handlers.PublicPricing {
	if publicPricing != nil {
		return publicPricing
	}
	return func() handlers.PublicPricing {
		svc := billingOf(env)
		if !svc.Enforced() {
			return handlers.BetaPricing()
		}
		return pricingFromBilling(svc.Pricing())
	}
}

// pricingFromBilling turns the billing plans into the public page's shape.
func pricingFromBilling(p billing.PublicPricing) handlers.PublicPricing {
	out := handlers.PublicPricing{
		Headline: "Plans for teams that ship an agent. Bring your own model key.",
		Plans:    []handlers.PublicPlan{},
		FAQ:      []handlers.PublicFAQ{},
	}
	for i, n := range p.Notes {
		if i == 0 {
			out.Note = n
			continue
		}
		out.FAQ = append(out.FAQ, handlers.PublicFAQ{Q: "How is it counted?", A: n})
	}
	for _, pl := range p.Plans {
		pp := handlers.PublicPlan{ID: pl.ID, Name: pl.Name, Summary: pl.Summary, Features: []string{}, Limits: []handlers.PublicLimit{}, CTA: "signup"}
		switch pl.PriceUnit {
		case "free":
			pp.Price, pp.Period = "Free", "forever"
		case "contact":
			pp.Price, pp.Period, pp.CTA = "Contact us", "custom", "contact"
		default:
			pp.Price, pp.Period = fmt.Sprintf("$%.0f", pl.PriceUSD), "per seat per month"
		}
		for _, f := range pl.Features {
			if w, ok := featureWords[f]; ok {
				pp.Features = append(pp.Features, w)
			} else {
				pp.Features = append(pp.Features, string(f))
			}
		}
		seats, minutes := "Unlimited", "Unlimited"
		if pl.MaxSeats > 0 {
			seats = fmt.Sprint(pl.MaxSeats)
		}
		if pl.SharedWorkerMinutes > 0 {
			minutes = fmt.Sprintf("%d minutes per month", pl.SharedWorkerMinutes)
		}
		pp.Limits = append(pp.Limits,
			handlers.PublicLimit{Name: "Members", Value: seats},
			handlers.PublicLimit{Name: "Shared worker", Value: minutes},
			handlers.PublicLimit{Name: "Model spend", Value: "Paid by you to your provider, on your own key"})
		out.Plans = append(out.Plans, pp)
	}
	return out
}
