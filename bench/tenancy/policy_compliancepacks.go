package tenancy

// Compliance packs (handlers/compliancepacks.go, gap G08): read-only
// downloads of the release.compliance job's output; viewers may read.
func init() {
	for k, r := range map[string]Rule{
		key("GET", api+"/compliance-packs"):        org(viewer),
		key("GET", api+"/compliance-packs/{name}"): org(viewer),
	} {
		OrgPolicy[k] = r
	}
}
