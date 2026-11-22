package tenancy

// Private image registry logins (lane Y10, handlers/registrycreds.go):
// viewers see hosts and user names, only admins add or remove a login.
// Passwords live in org secrets and are never readable.
func init() {
	OrgPolicy[key("GET", api+"/registry-credentials")] = org(viewer)
	OrgPolicy[key("POST", api+"/registry-credentials")] = org(admin)
	OrgPolicy[key("DELETE", api+"/registry-credentials/{id}")] = org(admin)
}
