package tenancy

// W4 features 2 and 6 (audit/W4_DESIGN_3_BIFROST.md 5.5 and 9.4): listing the
// org's Bifrost prompts and skills is for viewers, importing a skill version
// into an agent for members.
func init() {
	OrgPolicy[key("GET", api+"/gateway/prompts")] = org(viewer)
	OrgPolicy[key("GET", api+"/gateway/prompts/{id}/versions")] = org(viewer)
	OrgPolicy[key("GET", api+"/gateway/skills")] = org(viewer)
	OrgPolicy[key("GET", api+"/gateway/skills/{id}/versions")] = org(viewer)
	OrgPolicy[key("POST", api+"/agents/{id}/skills/from-bifrost")] = org(member)
}
