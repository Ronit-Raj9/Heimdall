package tenancy

// Wave 4 lane W4-45 (trace to environment): a member uploads a Claude Code
// or Codex session file into the org's failure inbox; anyone in the org
// reads the model's draft for an item.
func init() {
	OrgPolicy[key("POST", api+"/inbox/import-session")] = org(member)
	OrgPolicy[key("GET", api+"/inbox/{id}/proposal")] = org(viewer)
}
