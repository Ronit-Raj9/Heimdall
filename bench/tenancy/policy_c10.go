package tenancy

// C10: how Heimdall uses Bifrost (handlers/bifrostuse.go). Read only; the
// summary never carries the gateway's admin token, so viewers may read it.
func init() {
	for k, r := range map[string]Rule{
		key("GET", api+"/integrations/bifrost"):     org(viewer),
		key("GET", api+"/evaluations/{id}/bifrost"): org(viewer),
	} {
		OrgPolicy[k] = r
	}
}
