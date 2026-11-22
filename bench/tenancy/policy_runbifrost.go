package tenancy

// Through Bifrost run panel (lane C09, handlers/runbifrost.go): read only,
// scoped like GET /runs/{id}/routing. The hosted server resolves the run in
// the caller's org index first, so another org's run is a 404 before any
// Bifrost row or plugin usage row is read.
func init() {
	OrgPolicy[key("GET", api+"/runs/{id}/bifrost")] = org(viewer)
}
