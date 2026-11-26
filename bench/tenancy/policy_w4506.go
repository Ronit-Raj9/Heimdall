package tenancy

// Improvement runs, wave 4 (D5 WP6): resuming and continuing spend the
// org's keys, so they are for org admins. The continue estimate only reads,
// but every org POST needs at least a member (TestPolicyDefaults).
// Studies run several loops: starting one is for admins, reading for every
// member, and any member may stop one, like a single run.
func init() {
	OrgPolicy[key("POST", api+"/improvements/{id}/resume")] = org(admin)
	OrgPolicy[key("POST", api+"/improvements/{id}/continue")] = org(admin)
	OrgPolicy[key("POST", api+"/improvements/{id}/continue/estimate")] = org(member)
	OrgPolicy[key("POST", api+"/improvement-studies")] = org(admin)
	OrgPolicy[key("GET", api+"/improvement-studies")] = org(viewer)
	OrgPolicy[key("GET", api+"/improvement-studies/{id}")] = org(viewer)
	OrgPolicy[key("POST", api+"/improvement-studies/{id}/stop")] = org(member)
}
