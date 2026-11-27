package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
)

func TestJobsCountPerOrganization(t *testing.T) {
	r := testRunner(t)
	h := NewJobsHandler(testMode, r, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	a := &auth.Principal{Kind: auth.KindUser, UserID: "usr_m", OrgID: "org_a", Role: auth.RoleMember}
	b := &auth.Principal{Kind: auth.KindUser, UserID: "usr_o", OrgID: "org_b", Role: auth.RoleOwner}
	for range 2 {
		if resp := jobsRequest(t, reg, a, "POST", "/api/bench/jobs", `{"kind":"demo.echo","params":{"name":"ada"}}`); resp.StatusCode() != 202 {
			t.Fatalf("create %d %s", resp.StatusCode(), resp.Body())
		}
	}
	count := func(p *auth.Principal, q string) JobCount {
		resp := jobsRequest(t, reg, p, "GET", "/api/bench/jobs?fields=count"+q, "")
		var c JobCount
		if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &c) != nil {
			t.Fatalf("count %d %s", resp.StatusCode(), resp.Body())
		}
		return c
	}
	if c := count(a, ""); c.Count != 2 || c.More {
		t.Fatalf("org a %+v", c)
	}
	if c := count(a, "&kind=nope"); c.Count != 0 {
		t.Fatalf("filtered %+v", c)
	}
	if c := count(b, ""); c.Count != 0 {
		t.Fatalf("org b sees org a jobs: %+v", c)
	}
	if resp := jobsRequest(t, reg, a, "GET", "/api/bench/jobs?fields=count&status=bad", ""); resp.StatusCode() != 400 {
		t.Fatalf("bad status %d", resp.StatusCode())
	}
}
