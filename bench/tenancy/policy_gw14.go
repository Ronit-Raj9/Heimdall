package tenancy

// Fix wave 2 lane G14 (N11): removing an offline worker's record is a
// server admin route; signed-in users only, the handler answers 403
// unless the user is a server admin.
func init() {
	PlatformPolicy[key("POST", api+"/admin/containers/{org}/{id}/remove")] = userOnly()
}
