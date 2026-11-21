package tenancy

// Org secrets (web parity lane L06, handlers/secrets.go): viewers see the
// names, only admins store, rotate or delete. Values are never readable.
func init() {
	OrgPolicy[key("GET", api+"/secrets")] = org(viewer)
	OrgPolicy[key("POST", api+"/secrets")] = org(admin)
	OrgPolicy[key("PUT", api+"/secrets/{id}")] = org(admin)
	OrgPolicy[key("DELETE", api+"/secrets/{id}")] = org(admin)
}
