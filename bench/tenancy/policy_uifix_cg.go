package tenancy

// UI fix wave CG: read-only summaries built from routes a viewer already
// reads.
func init() {
	OrgPolicy[key("GET", api+"/home/summary")] = org(viewer)
}
