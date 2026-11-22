package tenancy

// Drain mode (Z11, handlers/drain.go): the platform router serves the drain
// routes and the handler checks the drain token or a server admin itself.
// The org router's copy is never reached (platform routes match first).
func init() {
	for _, m := range []string{"GET", "POST"} {
		PlatformPolicy[key(m, api+"/admin/drain")] = public()
		OrgPolicy[key(m, api+"/admin/drain")] = deny("drain mode is a platform route")
	}
}
