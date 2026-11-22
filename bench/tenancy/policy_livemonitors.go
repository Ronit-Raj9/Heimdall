package tenancy

// Live monitors (lane Z22, handlers/livemonitors.go): viewers read them,
// members change them; anyone holding a badge link reads that one badge
// (AccessShare: the token picks the org, the handler checks its secret).
func init() {
	OrgPolicy[key("GET", api+"/live-monitors")] = org(viewer)
	OrgPolicy[key("POST", api+"/live-monitors")] = org(member)
	OrgPolicy[key("GET", api+"/live-monitors/{id}")] = org(viewer)
	OrgPolicy[key("PUT", api+"/live-monitors/{id}")] = org(member)
	OrgPolicy[key("DELETE", api+"/live-monitors/{id}")] = org(member)
	OrgPolicy[key("POST", api+"/live-monitors/{id}/pause")] = org(member)
	OrgPolicy[key("POST", api+"/live-monitors/{id}/resume")] = org(member)
	OrgPolicy[key("POST", api+"/live-monitors/{id}/run")] = org(member)
	OrgPolicy[key("POST", api+"/live-monitors/{id}/badge")] = org(member)
	OrgPolicy[key("GET", api+"/live-monitor-badges/{token}/badge.svg")] = shareLink()
}
