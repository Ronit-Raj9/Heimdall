package tenancy

// X23: cross-run trace search and the Maxim integration
// (handlers/search.go, handlers/maxim.go). Viewers search and read the
// connection and a run's Maxim link; the connection holds an API key, so
// changing it is admin; sending a run is member.
func init() {
	for k, r := range map[string]Rule{
		key("GET", api+"/search"):                   org(viewer),
		key("GET", api+"/integrations/maxim"):       org(viewer),
		key("PUT", api+"/integrations/maxim"):       org(admin),
		key("DELETE", api+"/integrations/maxim"):    org(admin),
		key("POST", api+"/integrations/maxim/test"): org(admin),
		key("GET", api+"/runs/{id}/maxim"):          org(viewer),
		key("POST", api+"/runs/{id}/maxim/push"):    org(member),
	} {
		OrgPolicy[k] = r
	}
}
