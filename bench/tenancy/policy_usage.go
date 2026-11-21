package tenancy

// Usage and spend area (web parity L25): GET /usage is readable by any org
// member; GET /admin/usage is a platform route the handler limits to server
// admins (auth.IsServerAdmin).
func init() {
	OrgPolicy[key("GET", api+"/usage")] = org(viewer)
	PlatformPolicy[key("GET", api+"/admin/usage")] = userOnly()
}
