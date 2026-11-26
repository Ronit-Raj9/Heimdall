package tenancy

// Wave 4 lane I30 (design W4-43): findings. Viewers read them; members track,
// mark expected, reopen, rerun and check them.
func init() {
	OrgPolicy[key("GET", api+"/findings")] = org(viewer)
	OrgPolicy[key("GET", api+"/findings/{id}")] = org(viewer)
	OrgPolicy[key("POST", api+"/findings")] = org(member)
	OrgPolicy[key("POST", api+"/findings/{id}/expected")] = org(member)
	OrgPolicy[key("POST", api+"/findings/{id}/reopen")] = org(member)
	OrgPolicy[key("POST", api+"/findings/{id}/rerun")] = org(member)
	OrgPolicy[key("POST", api+"/findings/{id}/check")] = org(member)
}
