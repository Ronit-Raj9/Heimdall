package tenancy

// Prompt logging (lane X03, handlers/promptlogs.go): viewers see the setting
// and their organization's gateway log rows (metadata only); only admins
// change the setting or delete the rows.
func init() {
	OrgPolicy[key("GET", api+"/prompt-logging")] = org(viewer)
	OrgPolicy[key("PUT", api+"/prompt-logging")] = org(admin)
	OrgPolicy[key("GET", api+"/prompt-logs")] = org(viewer)
	OrgPolicy[key("DELETE", api+"/prompt-logs")] = org(admin)
}
