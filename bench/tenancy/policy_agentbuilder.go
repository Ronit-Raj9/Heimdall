package tenancy

// Agent builder (web parity L08): viewers read templates and configs;
// members validate, create and edit agents (POST is never open to viewers).
func init() {
	OrgPolicy[key("GET", api+"/agents/templates")] = org(viewer)
	OrgPolicy[key("POST", api+"/agents/from-template")] = org(member)
	OrgPolicy[key("POST", api+"/agents/config/validate")] = org(member)
	OrgPolicy[key("GET", api+"/agents/{id}/config")] = org(viewer)
	OrgPolicy[key("PUT", api+"/agents/{id}/config")] = org(member)
}
