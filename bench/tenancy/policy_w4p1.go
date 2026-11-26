package tenancy

// W4 Bifrost feature tests (audit/W4_DESIGN_3_BIFROST.md 3.6): reading
// reports is for viewers, drafting a test for members, and the connection
// to the org's own Bifrost for admins.
func init() {
	OrgPolicy[key("GET", api+"/gateway/features")] = org(viewer)
	OrgPolicy[key("POST", api+"/gateway/tests/draft")] = org(member)
	OrgPolicy[key("GET", api+"/experiments/{id}/gateway-effect")] = org(viewer)
	OrgPolicy[key("GET", api+"/gateway/effects")] = org(viewer)
	OrgPolicy[key("GET", api+"/gateway/report-card")] = org(viewer)
	OrgPolicy[key("GET", api+"/gateway/source")] = org(viewer)
	OrgPolicy[key("POST", api+"/gateway/source")] = org(admin)
	OrgPolicy[key("POST", api+"/gateway/source/remove")] = org(admin)
}
