package tenancy

// Red team (Z04, handlers/redteam.go): reading the catalog and the
// regressions is a viewer's; writing a benchmark, harvesting and reviewing
// change suite files, so they are a member's.
func init() {
	OrgPolicy[key("GET", api+"/redteam/catalog")] = org(viewer)
	OrgPolicy[key("POST", api+"/redteam/benchmark")] = org(member)
	OrgPolicy[key("GET", api+"/redteam/regressions")] = org(viewer)
	OrgPolicy[key("POST", api+"/redteam/regressions/harvest")] = org(member)
	OrgPolicy[key("POST", api+"/redteam/regressions/{task}/review")] = org(member)
}
