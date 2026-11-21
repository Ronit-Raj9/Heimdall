package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
)

// TestMaximSealHookRemembersOrgs: the service hook keeps each org's
// services and project root; a seal of an org never seen does nothing.
func TestMaximSealHookRemembersOrgs(t *testing.T) {
	root := t.TempDir()
	rememberMaximOrg(&app{cfg: &lib.Config{Root: root}}, &handlers.Services{OrgID: "org-x23"})
	maximOrgsMu.Lock()
	o, ok := maximOrgs["org-x23"]
	maximOrgsMu.Unlock()
	if !ok || o.root != root {
		t.Fatalf("org %+v %v", o, ok)
	}
	rememberMaximOrg(nil, &handlers.Services{OrgID: "nil-app"})
	maximOnSeal("never-seen", "r1", nil)
	maximOnSeal("org-x23", "r1", nil) // not connected: the send is a no-op
}
