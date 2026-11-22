package tenancy

// Fix wave 2 lane G18: the members a labelling queue can name as
// labellers, by user id and display name (handlers/annotation.go). Members
// read it; the names follow the display rule, so no email is listed.
func init() {
	OrgPolicy[key("GET", api+"/annotation/people")] = org(viewer)
}
