package tenancy

// Run control (handlers/runcontrol.go): pause, resume, live controls, run
// cancel and requeue, and reruns of failures. Reading the controls is for
// viewers; every change is member work.
func init() {
	OrgPolicy[key("GET", api+"/experiments/{id}/controls")] = org(viewer)
	OrgPolicy[key("PATCH", api+"/experiments/{id}/controls")] = org(member)
	OrgPolicy[key("POST", api+"/experiments/{id}/pause")] = org(member)
	OrgPolicy[key("POST", api+"/experiments/{id}/rerun-failures")] = org(member)
	OrgPolicy[key("POST", api+"/experiments/{id}/runs/{run}/cancel")] = org(member)
	OrgPolicy[key("POST", api+"/experiments/{id}/runs/{run}/requeue")] = org(member)
	OrgPolicy[key("POST", api+"/evaluations/{id}/pause")] = org(member)
	OrgPolicy[key("POST", api+"/evaluations/{id}/resume")] = org(member)
	OrgPolicy[key("POST", api+"/evaluations/{id}/rerun-failures")] = org(member)
}
