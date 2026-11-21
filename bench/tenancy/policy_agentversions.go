package tenancy

// Agent versions (A06): viewers list and read versions; members snapshot.
func init() {
	OrgPolicy[key("GET", api+"/agents/{id}/versions")] = org(viewer)
	OrgPolicy[key("GET", api+"/agents/{id}/versions/{n}")] = org(viewer)
	OrgPolicy[key("POST", api+"/agents/{id}/versions")] = org(member)
}
