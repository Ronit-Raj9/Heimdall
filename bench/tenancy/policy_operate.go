package tenancy

// Web parity lane L20 (Operate). The job kinds go through POST /jobs (member).
// Gap lane G09: the org's evidence backup zips.
func init() {
	OrgPolicy[key("GET", api+"/operate/status")] = org(viewer)
	OrgPolicy[key("GET", api+"/operate/backups")] = org(viewer)
	OrgPolicy[key("GET", api+"/operate/backups/{name}")] = org(viewer)
	OrgPolicy[key("DELETE", api+"/operate/backups/{name}")] = org(member)
}
