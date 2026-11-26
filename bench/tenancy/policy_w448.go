package tenancy

// Wave 4 lane I35 (W4-48): weekly issue sync. Turning a sync on, changing,
// removing or running it is admin work because a sync spends money every
// week without a person; anyone in the org may read the list.
func init() {
	OrgPolicy[key("GET", api+"/issuesync")] = org(viewer)
	OrgPolicy[key("POST", api+"/issuesync")] = org(admin)
	OrgPolicy[key("PUT", api+"/issuesync/{id}")] = org(admin)
	OrgPolicy[key("DELETE", api+"/issuesync/{id}")] = org(admin)
	OrgPolicy[key("POST", api+"/issuesync/{id}/run")] = org(admin)
}

// The cheat check starts runs on the org's keys, like rerun failures: a
// member action.
func init() {
	OrgPolicy[key("POST", api+"/evaluations/{id}/cheat-check")] = org(member)
}
