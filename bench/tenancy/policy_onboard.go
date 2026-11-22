package tenancy

// Add benchmark (lane Z20): members detect, draft, check and publish;
// viewers read drafts.
func init() {
	OrgPolicy[key("POST", api+"/onboard/upload")] = org(member)
	OrgPolicy[key("POST", api+"/onboard/detect")] = org(member)
	OrgPolicy[key("POST", api+"/onboard/inspect")] = org(member)
	OrgPolicy[key("GET", api+"/onboard/drafts")] = org(viewer)
	OrgPolicy[key("POST", api+"/onboard/drafts")] = org(member)
	OrgPolicy[key("GET", api+"/onboard/drafts/{set}")] = org(viewer)
	OrgPolicy[key("DELETE", api+"/onboard/drafts/{set}")] = org(member)
	OrgPolicy[key("PATCH", api+"/onboard/drafts/{set}/tasks/{task}")] = org(member)
	OrgPolicy[key("POST", api+"/onboard/drafts/{set}/ready")] = org(member)
	OrgPolicy[key("POST", api+"/onboard/drafts/{set}/publish")] = org(member)
}
