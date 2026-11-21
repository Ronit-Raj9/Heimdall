package tenancy

// Models and keys area (web parity L04, handlers/models.go). Anyone in the
// org may see which keys exist (never their values); members test keys;
// admins add, change and delete them.
func init() {
	OrgPolicy[key("GET", api+"/models")] = org(viewer)
	OrgPolicy[key("GET", api+"/models/providers")] = org(viewer) // A09: provider list for the key form
	OrgPolicy[key("GET", api+"/models/keys")] = org(viewer)
	OrgPolicy[key("POST", api+"/models/keys")] = org(admin)
	OrgPolicy[key("GET", api+"/models/keys/{id}")] = org(viewer)
	OrgPolicy[key("PUT", api+"/models/keys/{id}")] = org(admin)
	OrgPolicy[key("DELETE", api+"/models/keys/{id}")] = org(admin)
	OrgPolicy[key("POST", api+"/models/keys/{id}/test")] = org(member)
}
