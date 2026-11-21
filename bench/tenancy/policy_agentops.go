package tenancy

// Agent ops area (web parity lane L09): viewers read, members write.
func init() {
	OrgPolicy[key("GET", api+"/agents/{id}/skills")] = org(viewer)
	OrgPolicy[key("POST", api+"/agents/{id}/skills")] = org(member)
	OrgPolicy[key("DELETE", api+"/agents/{id}/skills/{name}")] = org(member)
	OrgPolicy[key("GET", api+"/agents/{id}/mcp")] = org(viewer)
	OrgPolicy[key("PUT", api+"/agents/{id}/mcp")] = org(member)
}
