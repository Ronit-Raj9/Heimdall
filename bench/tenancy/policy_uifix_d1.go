package tenancy

// UI fix wave D1 (decision 6): archive instead of delete. Archiving needs
// the role that may delete the object today: admin for an evaluation and
// a comparison (DELETE is admin) and for a run (deleting its comparison is
// admin); admin for an improvement run (it has no delete; creating one is
// admin); member for a finding (it has no delete; its other actions are
// member). Unarchive needs the same role as archive. Deleting a monitor
// test (safety rule test) is admin, like every other delete of stored data.
// The dispatcher writes the audit row of each.
func init() {
	for k, r := range map[string]Rule{
		key("POST", api+"/evaluations/{id}/archive"):       org(admin),
		key("POST", api+"/evaluations/{id}/unarchive"):     org(admin),
		key("POST", api+"/experiments/{id}/archive"):       org(admin),
		key("POST", api+"/experiments/{id}/unarchive"):     org(admin),
		key("POST", api+"/runs/{id}/archive"):              org(admin),
		key("POST", api+"/runs/{id}/unarchive"):            org(admin),
		key("POST", api+"/improvements/{id}/archive"):      org(admin),
		key("POST", api+"/improvements/{id}/unarchive"):    org(admin),
		key("POST", api+"/findings/{id}/archive"):          org(member),
		key("POST", api+"/findings/{id}/unarchive"):        org(member),
		key("DELETE", api+"/monitors/certifications/{id}"): org(admin),
	} {
		OrgPolicy[k] = r
	}
}
