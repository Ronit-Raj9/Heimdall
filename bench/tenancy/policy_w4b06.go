package tenancy

// Wave 4 lane I10 (N194): one suite's summary is read like the suite list.
func init() {
	OrgPolicy[key("GET", api+"/suites/{id}")] = org(viewer)
}
