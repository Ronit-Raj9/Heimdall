package tenancy

// Authoring helper (lane Z19): the review queue and quality checks read
// task metadata only; accepting a task and the held-out split write suite
// files.
func init() {
	OrgPolicy[key("GET", api+"/suites/{id}/generated")] = org(viewer)
	OrgPolicy[key("PUT", api+"/suites/{id}/tasks/{task}/review-state")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/quality")] = org(viewer)
	OrgPolicy[key("POST", api+"/suites/{id}/held-out")] = org(member)
}
