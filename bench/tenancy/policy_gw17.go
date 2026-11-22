package tenancy

// Fix wave 2, lane G17: copying or renaming an agent writes the org
// project's agent files (handlers/agentcopy.go); members may do it, viewers
// may not.
func init() {
	OrgPolicy[key("POST", api+"/agents/{id}/copy")] = org(member)
}
