package tenancy

// W4 Bifrost features 11 and 14 (audit/W4_DESIGN_3_BIFROST.md 14.4, 16.3):
// viewers read the agents registered in the org's Bifrost and the setting to
// paste for a comparison; members build an agent file from a registration.
func init() {
	OrgPolicy[key("GET", api+"/gateway/agents")] = org(viewer)
	OrgPolicy[key("POST", api+"/gateway/agents/{name}/import")] = org(member)
	OrgPolicy[key("GET", api+"/experiments/{id}/bifrost-setting")] = org(viewer)
}
