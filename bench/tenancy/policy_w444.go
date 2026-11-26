package tenancy

// W4-44 task categories: viewers read the labels; members label tasks.
func init() {
	OrgPolicy[key("GET", api+"/suites/{id}/categories")] = org(viewer)
	OrgPolicy[key("POST", api+"/suites/{id}/categories")] = org(member)
	OrgPolicy[key("PUT", api+"/suites/{id}/tasks/{task}/category")] = org(member)
}
