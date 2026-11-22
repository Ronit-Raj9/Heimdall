package tenancy

// The Connect box (lane Z01): viewers read the failure catalog and a
// verification's rows; members detect, save, verify, plan a first look and
// explain an error, as for creating an agent and testing an endpoint.
func init() {
	OrgPolicy[key("GET", api+"/connect/failures")] = org(viewer)
	OrgPolicy[key("POST", api+"/connect/explain")] = org(member)
	OrgPolicy[key("GET", api+"/connect/verify/{id}")] = org(viewer)
	OrgPolicy[key("POST", api+"/connect/detect")] = org(member)
	OrgPolicy[key("POST", api+"/connect/agents")] = org(member)
	OrgPolicy[key("POST", api+"/connect/verify")] = org(member)
	OrgPolicy[key("POST", api+"/connect/first-look")] = org(member)
}

// An evaluate-scoped token (the CLI and CI loop) may connect, verify and
// plan a first look, as it may register agent versions and start
// evaluations.
func init() {
	for _, p := range []string{"/connect/detect", "/connect/agents", "/connect/verify", "/connect/first-look", "/connect/explain"} {
		evaluateRoutes[key("POST", APIPrefix+p)] = true
	}
}
