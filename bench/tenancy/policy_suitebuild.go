package tenancy

// Own benchmarks from the website (lane X13): every route writes suite files.
func init() {
	OrgPolicy[key("POST", api+"/suites/{id}/rows")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/rows/imports/{import}")] = org(viewer) // lane Y13: background import progress (counts and problem lines only)
	OrgPolicy[key("PUT", api+"/suites/{id}/grader")] = org(member)
	OrgPolicy[key("PUT", api+"/suites/{id}/tasks/{task}/split")] = org(member)
}
