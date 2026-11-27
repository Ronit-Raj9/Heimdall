package tenancy

// UI fix wave G3: one page of a suite's tasks is read like the suite itself.
func init() {
	OrgPolicy[key("GET", api+"/suites/{id}/task-list")] = org(viewer)
}
