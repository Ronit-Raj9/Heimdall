package tenancy

// Agent import by platform id (Wave B lane Z16): viewers list the
// platforms; members import (it reads org secrets and writes an agent).
func init() {
	OrgPolicy[key("GET", api+"/agents/platforms")] = org(viewer)
	OrgPolicy[key("POST", api+"/agents/import-platform")] = org(member)
}
