package tenancy

// Wave 4 lane I43 (D5 A4.3): explaining a version of an improvement run
// reads only, like the run itself.
func init() {
	OrgPolicy[key("GET", api+"/improvements/{id}/versions/{n}/explain")] = org(viewer)
}
