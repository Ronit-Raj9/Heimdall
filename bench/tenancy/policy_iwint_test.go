package tenancy

import (
	"os"
	"regexp"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
)

// One rule for Delete (wave 4, lead's rule 15): the UI offers Delete on the
// benchmark and agent lists and on their own pages from the role the
// server's delete routes allow. The UI keeps that role in one constant,
// ui/lib/constants/deleteRole.ts; this test fails when the two part.
func TestDeleteRoleMatchesTheUI(t *testing.T) {
	b, err := os.ReadFile("../ui/lib/constants/deleteRole.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`DELETE_ROLE = "([a-z]+)" as const`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("no DELETE_ROLE in deleteRole.ts:\n%s", b)
	}
	ui := auth.Role(m[1])
	for _, k := range []string{key("DELETE", api+"/suites/{id}"), key("DELETE", api+"/agents/{id}")} {
		r, ok := OrgPolicy[k]
		if !ok || r.Access != AccessOrg || r.Min != ui {
			t.Errorf("%s: %+v (set %v), the UI offers Delete from %s", k, r, ok, ui)
		}
	}
}
