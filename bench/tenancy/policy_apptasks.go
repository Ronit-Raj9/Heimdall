package tenancy

// Tasks from your API (lane Z18): an OpenAPI file and a policy become a
// benchmark. GETs are viewer routes (the contract); one draft holds the
// tasks' hidden checks, so its handler refuses viewers, as suite files do.
func init() {
	OrgPolicy[key("POST", api+"/app-tasks")] = org(member)
	OrgPolicy[key("GET", api+"/app-tasks")] = org(viewer)
	OrgPolicy[key("GET", api+"/app-tasks/{id}")] = org(viewer)
	OrgPolicy[key("PUT", api+"/app-tasks/{id}/rules")] = org(member)
	OrgPolicy[key("POST", api+"/app-tasks/{id}/write")] = org(member)
	OrgPolicy[key("POST", api+"/app-tasks/{id}/review")] = org(member)
}
