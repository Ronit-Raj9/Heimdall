package tenancy

// Lane G01 (fix wave 2, N22): scanning one skill of an agent may install or
// uninstall it, so it is a member action like the upload.
func init() {
	OrgPolicy[key("POST", api+"/agents/{id}/skills/{name}/scan")] = org(member)
}
