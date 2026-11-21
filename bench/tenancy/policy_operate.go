package tenancy

// Web parity lane L20 (Operate). The job kinds go through POST /jobs (member).
func init() {
	OrgPolicy[key("GET", api+"/operate/status")] = org(viewer)
}
