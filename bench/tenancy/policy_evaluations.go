package tenancy

// Evaluations area (agent-first lanes A02, A03, A07): viewers read; members
// start, estimate and cancel, as for experiments.
func init() {
	OrgPolicy[key("GET", api+"/evaluations")] = org(viewer)
	OrgPolicy[key("GET", api+"/evaluations/{id}")] = org(viewer)
	OrgPolicy[key("POST", api+"/evaluations")] = org(member)
	OrgPolicy[key("POST", api+"/evaluations/estimate")] = org(member)
	OrgPolicy[key("POST", api+"/evaluations/{id}/cancel")] = org(member)
	OrgPolicy[key("GET", api+"/evaluation-packs")] = org(viewer)
	OrgPolicy[key("GET", api+"/faults")] = org(viewer) // X22: the fault catalog the evaluate wizard and Failure tests list
}

// Agent compatibility (agent-first lane A01): viewers read.
func init() {
	OrgPolicy[key("GET", api+"/agents/{id}/compatibility")] = org(viewer)
}
