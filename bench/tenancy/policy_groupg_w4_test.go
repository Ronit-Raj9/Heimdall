package tenancy

import (
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
)

// TestBuildersGroupPolicy: behaviour check rules, the replay opt-in and the
// weekly issue syncs are changed by admins only; members start a cheat
// check or send a replayed session to the Inbox; viewers read.
func TestBuildersGroupPolicy(t *testing.T) {
	for k, want := range map[string]auth.Role{
		key("PUT", api+"/behaviour-checks"):                            auth.RoleAdmin,
		key("PUT", api+"/replays/settings"):                            auth.RoleAdmin,
		key("POST", api+"/issuesync"):                                  auth.RoleAdmin,
		key("PUT", api+"/issuesync/{id}"):                              auth.RoleAdmin,
		key("DELETE", api+"/issuesync/{id}"):                           auth.RoleAdmin,
		key("POST", api+"/issuesync/{id}/run"):                         auth.RoleAdmin,
		key("POST", api+"/evaluations/{id}/cheat-check"):               auth.RoleMember,
		key("POST", api+"/replays/{id}/sessions/{recording}/to-inbox"): auth.RoleMember,
		key("GET", api+"/behaviour-checks"):                            auth.RoleViewer,
		key("GET", api+"/experiments/{id}/behaviour"):                  auth.RoleViewer,
		key("GET", api+"/replays"):                                     auth.RoleViewer,
		key("GET", api+"/investigations"):                              auth.RoleViewer,
		key("GET", api+"/validity"):                                    auth.RoleViewer,
	} {
		r, ok := OrgPolicy[k]
		if !ok || r.Access != AccessOrg || r.Min != want {
			t.Errorf("%s = %+v, want org(%s)", k, r, want)
		}
	}
}
