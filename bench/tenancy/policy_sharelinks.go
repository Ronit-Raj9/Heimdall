package tenancy

// Share links (lane X14, handlers/sharelinks.go): members create and revoke
// links, viewers list them and export report cards; anyone holding a link
// reads the one item it names (AccessShare: the token picks the org, the
// handler verifies it).
func init() {
	OrgPolicy[key("POST", api+"/shares")] = org(member)
	OrgPolicy[key("GET", api+"/shares")] = org(viewer)
	OrgPolicy[key("POST", api+"/shares/{id}/revoke")] = org(member)
	OrgPolicy[key("GET", api+"/shared/{token}")] = shareLink()
	OrgPolicy[key("GET", api+"/shared/{token}/badge.svg")] = shareLink()
	OrgPolicy[key("GET", api+"/shared/{token}/export")] = shareLink()
	OrgPolicy[key("GET", api+"/evaluations/{id}/report/export")] = org(viewer)
	OrgPolicy[key("GET", api+"/evaluations/{id}/reproduce")] = org(viewer)
}
