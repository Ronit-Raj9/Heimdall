package tenancy

// Fix wave lane F17: pause and resume schedules and drift monitors, and
// edit a drift monitor. Members write.
func init() {
	OrgPolicy[key("POST", api+"/schedules/{name}/pause")] = org(member)
	OrgPolicy[key("POST", api+"/schedules/{name}/resume")] = org(member)
	OrgPolicy[key("POST", api+"/canaries/{name}/pause")] = org(member)
	OrgPolicy[key("POST", api+"/canaries/{name}/resume")] = org(member)
	OrgPolicy[key("PUT", api+"/canaries/{name}")] = org(member)
}
