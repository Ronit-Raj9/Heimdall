package tenancy

// Round 7 lane T3: build a task from a failure. Anyone in the org reads the
// build; a member (who may edit benchmarks) starts, approves or rejects it.
func init() {
	OrgPolicy[key("GET", api+"/inbox/{id}/build-task")] = org(viewer)
	OrgPolicy[key("POST", api+"/inbox/{id}/build-task")] = org(member)
	OrgPolicy[key("POST", api+"/inbox/{id}/build-task/approve")] = org(member)
	OrgPolicy[key("POST", api+"/inbox/{id}/build-task/reject")] = org(member)
}
