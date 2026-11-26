package tenancy

// Lane I28 (W4-41): first divergence of two runs and behaviour checks.
// Reading them is a viewer action; changing the org's checks is an admin one.
func init() {
	OrgPolicy[key("GET", api+"/pairs/{id}/divergence")] = org(viewer)
	OrgPolicy[key("GET", api+"/runs/{id}/divergence/{other}")] = org(viewer)
	OrgPolicy[key("GET", api+"/behaviour-checks")] = org(viewer)
	OrgPolicy[key("PUT", api+"/behaviour-checks")] = org(admin)
	OrgPolicy[key("GET", api+"/runs/{id}/behaviour")] = org(viewer)
	OrgPolicy[key("GET", api+"/experiments/{id}/behaviour")] = org(viewer)
}
