package tenancy

// Lane Y15: annotation queues, the dataset API, platform imports and the
// benchmark data policy. Viewers read; members write. Inside the routes,
// queue settings, items and decisions need the queue's creator or an org
// admin, labels need a signed-in person, and changing who may see a
// benchmark needs an org admin (handlers/annotation.go,
// handlers/benchmarkaccess.go).
func init() {
	q := api + "/annotation/queues"
	OrgPolicy[key("GET", q)] = org(viewer)
	OrgPolicy[key("POST", q)] = org(member)
	OrgPolicy[key("GET", q+"/{id}")] = org(viewer)
	OrgPolicy[key("PUT", q+"/{id}")] = org(member)
	OrgPolicy[key("DELETE", q+"/{id}")] = org(member)
	OrgPolicy[key("POST", q+"/{id}/items")] = org(member)
	OrgPolicy[key("GET", q+"/{id}/items")] = org(viewer)
	OrgPolicy[key("POST", q+"/{id}/assign")] = org(member)
	OrgPolicy[key("POST", q+"/{id}/next")] = org(member)
	OrgPolicy[key("POST", q+"/{id}/items/{item}/labels")] = org(member)
	OrgPolicy[key("POST", q+"/{id}/items/{item}/resolve")] = org(member)
	OrgPolicy[key("GET", q+"/{id}/agreement")] = org(viewer)

	OrgPolicy[key("POST", api+"/suites/{id}/dataset/tasks")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/dataset/tasks")] = org(viewer)
	OrgPolicy[key("DELETE", api+"/suites/{id}/dataset/tasks/{task}")] = org(member)
	OrgPolicy[key("GET", api+"/suites/{id}/dataset/versions")] = org(viewer)
	OrgPolicy[key("GET", api+"/suites/{id}/dataset/versions/{n}")] = org(viewer)

	OrgPolicy[key("GET", api+"/imports/platforms")] = org(viewer)
	OrgPolicy[key("POST", api+"/imports/platforms")] = org(member)

	OrgPolicy[key("GET", api+"/data-policies")] = org(viewer)
	OrgPolicy[key("GET", api+"/suites/{id}/data-policy")] = org(viewer)
	OrgPolicy[key("PUT", api+"/suites/{id}/data-policy")] = org(member)
}
