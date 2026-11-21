package tenancy

func init() {
	OrgPolicy[key("GET", api+"/benchmarks/catalog")] = org(viewer)
	OrgPolicy[key("POST", api+"/benchmarks/catalog/{id}/add")] = org(member)
}
