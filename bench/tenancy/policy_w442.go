package tenancy

// Wave 4 lane W4-42: the findings investigations are read by any member of
// the org, viewers included.
func init() {
	OrgPolicy[key("GET", api+"/investigations")] = org(viewer)
	OrgPolicy[key("GET", api+"/investigations/{id}")] = org(viewer)
}
