package tenancy

// Web parity lane L17: schedules, webhook alerts and canary definitions.
func init() {
	OrgPolicy[key("GET", api+"/schedules")] = org(viewer)
	OrgPolicy[key("PUT", api+"/schedules")] = org(member)
	OrgPolicy[key("POST", api+"/schedules/test-alert")] = org(member)
	OrgPolicy[key("POST", api+"/schedules/{name}/run")] = org(member)
	OrgPolicy[key("POST", api+"/canaries")] = org(member)
	OrgPolicy[key("DELETE", api+"/canaries/{name}")] = org(member)
}
