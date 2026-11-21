package tenancy

// L26 CLI push/pull: the manifest is a read like GET /project/files.
func init() {
	OrgPolicy[key("GET", api+"/project/manifest")] = org(viewer)
}
