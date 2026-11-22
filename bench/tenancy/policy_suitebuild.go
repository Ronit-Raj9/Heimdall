package tenancy

// Own benchmarks from the website (lane X13): every route writes suite files.
func init() {
	OrgPolicy[key("POST", api+"/suites/{id}/rows")] = org(member)
	OrgPolicy[key("PUT", api+"/suites/{id}/grader")] = org(member)
	OrgPolicy[key("PUT", api+"/suites/{id}/tasks/{task}/split")] = org(member)
}

// Scenario rows (lane Y14): support tasks from rows, written as suite files.
func init() {
	OrgPolicy[key("POST", api+"/suites/{id}/scenario-rows")] = org(member)
}

// Stateful mocks from OpenAPI (lane Y14): writes services/<id>/ of a suite.
func init() {
	OrgPolicy[key("POST", api+"/suites/{id}/mocks")] = org(member)
}
