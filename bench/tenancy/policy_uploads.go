package tenancy

// Uploads area (web parity L24): zip uploads write the org's evidence store
// or project folder, so they need a member; viewers cannot upload.
func init() {
	OrgPolicy[key("POST", api+"/uploads/runs")] = org(member)
	OrgPolicy[key("POST", api+"/uploads/project")] = org(member)
	OrgPolicy[key("POST", api+"/uploads/agent")] = org(member)
}
