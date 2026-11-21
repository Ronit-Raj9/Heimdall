package tenancy

// Org project transfer (PROD2 P0 isolation): only a runner of the
// organization fetches its project; the shared runner no longer mounts the
// server's data volume.
func init() {
	OrgPolicy[key("GET", api+"/workers/project")] = runner(false)
}
