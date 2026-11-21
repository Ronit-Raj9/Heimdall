package tenancy

// Suite ops area (web parity lane L11): quarantine and restore write the
// suite's health sidecar, so they need a member.
func init() {
	OrgPolicy[key("POST", api+"/suites/{id}/tasks/{task}/quarantine")] = org(member)
	OrgPolicy[key("POST", api+"/suites/{id}/tasks/{task}/restore")] = org(member)
}
