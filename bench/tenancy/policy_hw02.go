package tenancy

// Lane H02 (fix wave 3, N288): deleting a benchmark of the organization is a
// member action, like deleting an agent or a benchmark file (D16).
func init() {
	OrgPolicy[key("DELETE", api+"/suites/{id}")] = org(member)
}
