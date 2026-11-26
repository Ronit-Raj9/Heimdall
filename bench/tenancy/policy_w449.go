package tenancy

// W4-49: the paged step routes and the validity ledger are viewer reads (a
// viewer marks an alert seen for themself). The feedback route is public
// here: the handler checks the org's own ingest token, as the hosted ingest
// route does, rate limited per org.
func init() {
	OrgPolicy[key("GET", api+"/runs/{id}/steps/page")] = org(viewer)
	OrgPolicy[key("GET", api+"/runs/{id}/step/{seq}")] = org(viewer)
	OrgPolicy[key("GET", api+"/validity")] = org(viewer)
	OrgPolicy[key("GET", api+"/experiments/{id}/validity")] = org(viewer)
	OrgPolicy[key("POST", api+"/validity/{id}/seen")] = org(viewer)
	PlatformPolicy[key("POST", api+"/orgs/{org}/feedback")] = public() // the handler checks the org's ingest token, rate limited per org
}
