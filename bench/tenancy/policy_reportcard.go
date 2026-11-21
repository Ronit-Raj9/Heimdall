package tenancy

// Report card area (agent-first lane A04): viewers read.
func init() {
	OrgPolicy[key("GET", api+"/evaluations/{id}/report")] = org(viewer)
	OrgPolicy[key("GET", api+"/agents/{id}/report-card")] = org(viewer)
}
