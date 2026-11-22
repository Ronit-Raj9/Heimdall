package tenancy

// Lane F13: deleting an agent removes its settings file from the org
// project (handlers/agentdelete.go); members may do it, viewers may not.
func init() {
	OrgPolicy[key("DELETE", api+"/agents/{id}")] = org(member)
}
