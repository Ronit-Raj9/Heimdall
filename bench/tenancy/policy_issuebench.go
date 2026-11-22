package tenancy

// Issues to benchmark (lane G01, handlers/issuebench_scan.go): the scan is
// a free dry run over the org's GitHub connection; members may run it.
func init() {
	PlatformPolicy[key("POST", api+"/issuebench/scan")] = org(member)
}

// Issues to benchmark (lane G06): viewers read runs; members scan, start,
// review and retry. Building sends repository code to the org's model
// provider, so start needs explicit consent in the body (checked by the
// pipeline).
func init() {
	OrgPolicy[key("GET", api+"/issuebench/readiness")] = org(viewer)
	OrgPolicy[key("GET", api+"/issuebench/runs")] = org(viewer)
	OrgPolicy[key("POST", api+"/issuebench/runs")] = org(member)
	OrgPolicy[key("GET", api+"/issuebench/runs/{id}")] = org(viewer)
	OrgPolicy[key("POST", api+"/issuebench/runs/{id}/start")] = org(member)
	OrgPolicy[key("POST", api+"/issuebench/runs/{id}/cancel")] = org(member)
	OrgPolicy[key("POST", api+"/issuebench/runs/{id}/issues/{issue}/review")] = org(member)
	OrgPolicy[key("POST", api+"/issuebench/runs/{id}/issues/{issue}/retry")] = org(member)
}
