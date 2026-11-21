package tenancy

// Improvement runs (handlers/improve.go): reads for every org member;
// starting one and approving a round are for org admins, since they spend
// the org's keys. On a hosted server each experiment of the run goes through
// the org's launcher like any other org run: its own model keys, the daily
// caps and the org's default worker (cmd/bifrost-bench serveHostedImprovements).
// Any member may stop one.
func init() {
	OrgPolicy[key("GET", api+"/improvements")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvements/{id}")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvements/{id}/rounds/{k}")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvements/{id}/lineage")] = org(viewer)
	OrgPolicy[key("POST", api+"/improvements")] = org(admin)
	OrgPolicy[key("POST", api+"/improvements/{id}/rounds/{k}/approve")] = org(admin)
	OrgPolicy[key("POST", api+"/improvements/{id}/stop")] = org(member)
}
