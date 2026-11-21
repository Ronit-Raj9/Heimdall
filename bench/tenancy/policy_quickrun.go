package tenancy

// QuickRun area (web parity L28): writes a project file and starts an
// experiment, so members only.
func init() {
	OrgPolicy[key("POST", api+"/quickrun")] = org(member)
}
