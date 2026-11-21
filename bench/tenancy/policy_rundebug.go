package tenancy

// Run debugging (X20b): reading calls and breakpoints is a view; starting a
// replay, fork or debug run and releasing a held call change runs, so members only.
func init() {
	OrgPolicy[key("GET", api+"/runs/{id}/calls")] = org(viewer)
	OrgPolicy[key("GET", api+"/runs/{id}/calls/{request}")] = org(viewer)
	OrgPolicy[key("GET", api+"/runs/{id}/holds")] = org(viewer)
	OrgPolicy[key("POST", api+"/runs/{id}/replay")] = org(member)
	OrgPolicy[key("POST", api+"/runs/{id}/fork")] = org(member)
	OrgPolicy[key("POST", api+"/runs/{id}/debug")] = org(member)
	OrgPolicy[key("POST", api+"/runs/{id}/holds/release")] = org(member)
}
