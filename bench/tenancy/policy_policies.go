package tenancy

// Release policy editor routes (web parity lane L16, handlers/policies.go):
// any org viewer reads, a member validates and writes.
func init() {
	OrgPolicy[key("GET", api+"/policies")] = org(viewer)
	OrgPolicy[key("POST", api+"/policies/validate")] = org(member)
	OrgPolicy[key("GET", api+"/policies/{name}")] = org(viewer)
	OrgPolicy[key("PUT", api+"/policies/{name}")] = org(member)
	OrgPolicy[key("DELETE", api+"/policies/{name}")] = org(member)
}
