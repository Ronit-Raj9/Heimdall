package tenancy

// Z06: Maxim production failures to regression tests
// (handlers/maximtasks.go). Viewers read imports; pulling, drafting and
// reviewing tasks is member work, like the failure inbox.
func init() {
	base := api + "/integrations/maxim/batches"
	task := base + "/{id}/tasks/{suite}/{task}"
	for k, r := range map[string]Rule{
		key("GET", base):                org(viewer),
		key("POST", base):               org(member),
		key("GET", base+"/{id}"):        org(viewer),
		key("POST", base+"/{id}/draft"): org(member),
		key("POST", task+"/accept"):     org(member),
		key("POST", task+"/reject"):     org(member),
		key("POST", task+"/readiness"):  org(member),
	} {
		OrgPolicy[k] = r
	}
}
