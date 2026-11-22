package tenancy

// Agents as code and endpoint mapping (lane Y10): members create agents,
// test saved agents and preview the requests of an HTTP endpoint agent;
// viewers read the platform presets.
func init() {
	OrgPolicy[key("POST", api+"/agents")] = org(member)
	OrgPolicy[key("POST", api+"/agents/{id}/test")] = org(member)
	OrgPolicy[key("GET", api+"/agents/endpoint-presets")] = org(viewer)
	OrgPolicy[key("POST", api+"/agents/endpoint-preview")] = org(member)
}
