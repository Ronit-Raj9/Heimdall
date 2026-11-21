package tenancy

// Integrations area (handlers/integrations.go, web parity L19). Webhooks hold
// signing secrets, so changing them is admin; testing one and managing
// export files is member; reading is viewer.
func init() {
	for k, r := range map[string]Rule{
		key("GET", api+"/integrations/webhooks"):            org(viewer),
		key("POST", api+"/integrations/webhooks"):           org(admin),
		key("PUT", api+"/integrations/webhooks/{id}"):       org(admin),
		key("DELETE", api+"/integrations/webhooks/{id}"):    org(admin),
		key("POST", api+"/integrations/webhooks/{id}/test"): org(member),
		key("GET", api+"/integrations/deliveries"):          org(viewer),
		key("GET", api+"/integrations/exports"):             org(viewer),
		key("GET", api+"/integrations/exports/{name}"):      org(viewer),
		key("DELETE", api+"/integrations/exports/{name}"):   org(member),
		key("GET", api+"/integrations/ci/workflow"):         org(viewer),
	} {
		OrgPolicy[k] = r
	}
}
