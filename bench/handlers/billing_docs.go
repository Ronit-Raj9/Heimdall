package handlers

import "github.com/maximhq/bifrost/bench/billing"

// Plans, metering and billing (lane Y16): route docs. These are platform
// routes of the hosted server.
func init() {
	usageQuery := []QueryDoc{
		{Name: "from", Description: "First day, YYYY-MM-DD (UTC); default 29 days before to"},
		{Name: "to", Description: "Last day, inclusive, YYYY-MM-DD (UTC); default today"},
	}
	b := APIPrefix + "/billing"
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "GET", Path: b, Tag: "billing",
			Summary: "The organization's plan, its meters against the plan's limits, and whether billing is on", Response: billing.View{}},
		RouteDoc{Method: "GET", Path: b + "/usage", Tag: "billing", Query: usageQuery,
			Summary: "Metered usage per day: shared worker minutes, runs, evaluations, storage and judge calls on operator keys", Response: billing.UsageReport{}},
		RouteDoc{Method: "POST", Path: b + "/checkout", Tag: "billing", OptionalBody: true,
			Summary: "Start a Stripe Checkout for the Team plan (admins); answers the URL to open", Response: CheckoutResponse{}},
		RouteDoc{Method: "POST", Path: b + "/portal", Tag: "billing", OptionalBody: true,
			Summary: "Open the Stripe customer portal (admins); answers the URL to open", Response: CheckoutResponse{}},
		RouteDoc{Method: "GET", Path: b + "/invoices", Tag: "billing",
			Summary: "The organization's newest invoices (admins)", Response: InvoiceList{}},
		RouteDoc{Method: "POST", Path: b + "/seats/sync", Tag: "billing", OptionalBody: true,
			Summary: "Set the Team subscription's paid seats to the member count (admins)", Response: billing.SeatSync{}},
		RouteDoc{Method: "POST", Path: b + "/webhook", Tag: "billing", RawRequest: "application/json",
			Summary: "Stripe webhook events; the Stripe-Signature header is verified with the endpoint's signing secret", Response: billing.WebhookResult{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/admin/billing/{id}", Tag: "billing", Request: SetPlanBody{},
			Summary: "Set an organization's plan by hand (server admins only)", Response: AdminBillingAccount{}},
		RouteDoc{Method: "GET", Path: PublicPricingPath, Tag: "billing",
			Summary: "Plans, prices, limits and features for the public pricing page (no sign-in)", Response: billing.PublicPricing{}},
	)
}
