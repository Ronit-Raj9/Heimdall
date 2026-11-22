package tenancy

// Plans, metering and billing (lane Y16). Members read the plan and usage;
// admins start checkout, open the portal, read invoices and sync seats. The
// webhook is public: its handler verifies the Stripe signature. Setting a
// plan by hand is a platform route the handler limits to server admins.
// The pricing data is public for the pricing page.
func init() {
	PlatformPolicy[key("GET", api+"/billing")] = org(viewer)
	PlatformPolicy[key("GET", api+"/billing/usage")] = org(viewer)
	PlatformPolicy[key("POST", api+"/billing/checkout")] = org(admin)
	PlatformPolicy[key("POST", api+"/billing/portal")] = org(admin)
	PlatformPolicy[key("GET", api+"/billing/invoices")] = org(admin)
	PlatformPolicy[key("POST", api+"/billing/seats/sync")] = org(admin)
	PlatformPolicy[key("POST", api+"/billing/webhook")] = public()
	PlatformPolicy[key("PUT", api+"/admin/billing/{id}")] = userOnly()
	PlatformPolicy[key("GET", "/api/public/pricing")] = public()
}
