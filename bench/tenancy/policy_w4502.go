package tenancy

// W4 design 5 (A2.9): the improvement estimate changes nothing, so any
// member of the org who can read improvement runs may ask for one.
func init() {
	OrgPolicy[key("POST", api+"/improvements/estimate")] = org(viewer)
}
