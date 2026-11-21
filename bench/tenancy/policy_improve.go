package tenancy

// Improvement runs (handlers/improve.go): reads for every org member; a
// create or an approval runs agents, which a hosted server never does.
func init() {
	OrgPolicy[key("GET", api+"/improvements")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvements/{id}")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvements/{id}/rounds/{k}")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvements/{id}/lineage")] = org(viewer)
	OrgPolicy[key("POST", api+"/improvements")] = deny("an improvement run runs agents; hosted servers run none (use a runner)")
	OrgPolicy[key("POST", api+"/improvements/{id}/rounds/{k}/approve")] = deny("approving a round continues an improvement run, which runs agents; hosted servers run none (use a runner)")
	OrgPolicy[key("POST", api+"/improvements/{id}/stop")] = org(member)
}
