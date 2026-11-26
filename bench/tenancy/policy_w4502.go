package tenancy

// W4 design 5 (A2.9): the improvement estimate changes nothing, but every
// POST of the org table needs at least member, like the evaluation estimate.
func init() {
	OrgPolicy[key("POST", api+"/improvements/estimate")] = org(member)
}
