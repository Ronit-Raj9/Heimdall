package tenancy

// Suite authoring area (web parity L10). Reads are viewer routes by contract;
// the file read handlers refuse a viewer themselves, since suite files hold
// hidden tests and reference solutions.
func init() {
	OrgPolicy[key("GET", api+"/suite-templates")] = org(viewer)
	OrgPolicy[key("GET", api+"/suites/{id}/lint")] = org(viewer)
	OrgPolicy[key("POST", api+"/suites")] = org(member)
	OrgPolicy[key("POST", api+"/suites/{id}/tasks")] = org(member)
	OrgPolicy[key("POST", api+"/suites/{id}/upload")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/files")] = org(viewer)
	OrgPolicy[key("GET", api+"/suites/{id}/files/{path:*}")] = org(viewer)
	OrgPolicy[key("PUT", api+"/suites/{id}/files/{path:*}")] = org(member)
	OrgPolicy[key("DELETE", api+"/suites/{id}/files/{path:*}")] = org(member)
}
