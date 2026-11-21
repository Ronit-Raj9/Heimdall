package tenancy

import (
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
)

// A viewer reads project files; only members and above save or delete them.
func TestProjectFilesPolicy(t *testing.T) {
	for k, want := range map[string]auth.Role{
		key("GET", api+"/project/files"):                    auth.RoleViewer,
		key("GET", api+"/project/files/{area}/{path:*}"):    auth.RoleViewer,
		key("GET", api+"/project/versions/{area}/{path:*}"): auth.RoleViewer,
		key("GET", api+"/project/export.zip"):               auth.RoleViewer,
		key("PUT", api+"/project/files/{area}/{path:*}"):    auth.RoleMember,
		key("DELETE", api+"/project/files/{area}/{path:*}"): auth.RoleMember,
	} {
		r, ok := OrgPolicy[k]
		if !ok || r.Access != AccessOrg || r.Min != want {
			t.Errorf("%s = %+v, want org(%s)", k, r, want)
		}
	}
}
