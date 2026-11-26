package tenancy

import (
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
)

// Group H security review: what spends the org's keys (create, approve,
// resume, continue, a study) is for org admins; the two estimates change
// nothing and are member POSTs; reads are for viewers.
func TestImprovementRoutesRoles(t *testing.T) {
	for k, want := range map[string]auth.Role{
		key("POST", api+"/improvements"):                          admin,
		key("POST", api+"/improvements/{id}/rounds/{k}/approve"):  admin,
		key("POST", api+"/improvements/{id}/resume"):              admin,
		key("POST", api+"/improvements/{id}/continue"):            admin,
		key("POST", api+"/improvement-studies"):                   admin,
		key("POST", api+"/improvements/estimate"):                 member,
		key("POST", api+"/improvements/{id}/continue/estimate"):   member,
		key("POST", api+"/improvements/{id}/stop"):                member,
		key("GET", api+"/improvements/{id}"):                      viewer,
		key("GET", api+"/improvements/{id}/versions/{n}/explain"): viewer,
		key("GET", api+"/improvement-studies/{id}"):               viewer,
	} {
		r, ok := OrgPolicy[k]
		if !ok || r.Access != AccessOrg || r.Min != want {
			t.Errorf("%s: %+v (set %v), want org %s", k, r, ok, want)
		}
	}
}
