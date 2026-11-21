package tenancy

// Project files area (web parity L01, handlers/projectfiles.go): viewers
// read, list, see versions and export; members save and delete.
func init() {
	OrgPolicy[key("GET", api+"/project/files")] = org(viewer)
	OrgPolicy[key("GET", api+"/project/files/{area}/{path:*}")] = org(viewer)
	OrgPolicy[key("PUT", api+"/project/files/{area}/{path:*}")] = org(member)
	OrgPolicy[key("DELETE", api+"/project/files/{area}/{path:*}")] = org(member)
	OrgPolicy[key("GET", api+"/project/versions/{area}/{path:*}")] = org(viewer)
	OrgPolicy[key("GET", api+"/project/export.zip")] = org(viewer)
}
