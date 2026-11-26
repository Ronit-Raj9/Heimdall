package tenancy

// W4 Bifrost feature 10 (audit/W4_DESIGN_3_BIFROST.md 13.4): reading the
// gateway time of a comparison is for viewers.
func init() {
	OrgPolicy[key("GET", api+"/experiments/{id}/gateway-overhead")] = org(viewer)
}
