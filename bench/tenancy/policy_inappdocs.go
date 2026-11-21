package tenancy

// In-app guides (handlers/inappdocs.go): read-only, any member or public-read visitor.
func init() {
	OrgPolicy[key("GET", api+"/docs")] = org(viewer)
	OrgPolicy[key("GET", api+"/docs/{name}")] = org(viewer)
}
