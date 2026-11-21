package tenancy

// Server admin area (web parity L23): signed-in users only; the handlers
// answer 403 unless the user is listed in HEIMDALL_SERVER_ADMINS.
func init() {
	for _, k := range []string{
		key("GET", api+"/admin/overview"),
		key("GET", api+"/admin/users"),
		key("POST", api+"/admin/users/{id}/disable"),
		key("GET", api+"/admin/orgs"),
		key("GET", api+"/admin/orgs/{id}/quota"),
		key("POST", api+"/admin/orgs/{id}/quota"),
		key("GET", api+"/admin/killswitch"),
		key("POST", api+"/admin/killswitch"),
		key("GET", api+"/admin/containers"),
	} {
		PlatformPolicy[k] = userOnly()
	}
}
