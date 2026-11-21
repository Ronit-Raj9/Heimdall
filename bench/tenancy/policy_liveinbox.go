package tenancy

// Web parity lane L18 (live scoring and inbox) and gap G02 (hosted inbox
// webhook). The webhook setup read is for viewers. The local ingest route
// (POST /inbox/ingest, the webhook source's server-side token) is not how
// the hosted server takes pushes: it takes them per org at
// /orgs/{org}/inbox/ingest, where the handler checks the org's own ingest
// token (handlers/inboxingest.go), so the route is public here. Admins
// create and revoke ingest tokens; viewers read the list (never a token).
func init() {
	OrgPolicy[key("GET", api+"/inbox/webhook")] = org(viewer)
	OrgPolicy[key("POST", api+"/inbox/ingest")] = deny("the hosted server takes pushes per org: POST /api/bench/orgs/{org}/inbox/ingest with an ingest token from the Inbox page (Push failures in)")
	PlatformPolicy[key("GET", api+"/inbox/tokens")] = org(viewer)
	PlatformPolicy[key("POST", api+"/inbox/tokens")] = org(admin)
	PlatformPolicy[key("DELETE", api+"/inbox/tokens/{id}")] = org(admin)
	PlatformPolicy[key("POST", api+"/orgs/{org}/inbox/ingest")] = public() // the handler checks the org's ingest token, rate limited per org
	OrgPolicy[key("GET", api+"/liveinbox/pull")] = org(viewer)
}
