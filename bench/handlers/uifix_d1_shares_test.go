package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

func shareCallAs(r *router.Router, method, uri string, body any, role auth.Role) *fasthttp.Response {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if body != nil {
		b, _ := json.Marshal(body)
		ctx.Request.SetBody(b)
	}
	auth.SetPrincipal(&ctx, &auth.Principal{Kind: auth.KindUser, UserID: "usr_" + string(role), OrgID: auth.LocalOrgID, Role: role, EmailVerified: true})
	r.Handler(&ctx)
	var out fasthttp.Response
	ctx.Response.CopyTo(&out)
	return &out
}

// TestShareExpiryRules (decision 7): no expiry named gives 7 days; never
// (0) is refused to a member with the reason and allowed to an admin.
func TestShareExpiryRules(t *testing.T) {
	r, _ := shareRouter(t)
	res := shareCallAs(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"}, auth.RoleMember)
	sv := d1Decode[ShareView](t, res)
	if res.StatusCode() != 201 || sv.ExpiresAt == nil {
		t.Fatalf("default: %d %s", res.StatusCode(), res.Body())
	}
	if d := time.Until(*sv.ExpiresAt); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("default expiry %v, want 7 days", d)
	}
	res = shareCallAs(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1", "expires_in_days": 0}, auth.RoleMember)
	if res.StatusCode() != 403 || !strings.Contains(string(res.Body()), "never expires") {
		t.Fatalf("member never: %d %s", res.StatusCode(), res.Body())
	}
	res = shareCallAs(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1", "expires_in_days": 0}, auth.RoleAdmin)
	if sv := d1Decode[ShareView](t, res); res.StatusCode() != 201 || sv.ExpiresAt != nil {
		t.Fatalf("admin never: %d %s", res.StatusCode(), res.Body())
	}
	res = shareCallAs(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1", "expires_in_days": 30}, auth.RoleMember)
	if res.StatusCode() != 201 {
		t.Fatalf("member 30 days: %d %s", res.StatusCode(), res.Body())
	}
}

// TestShareListRoles (decision 7): the unfiltered list is admin only; the
// filtered list answers every role; a viewer never gets token, path or url.
func TestShareListRoles(t *testing.T) {
	r, _ := shareRouter(t)
	if res := shareCallAs(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"}, auth.RoleMember); res.StatusCode() != 201 {
		t.Fatalf("create: %d %s", res.StatusCode(), res.Body())
	}
	all, filtered := APIPrefix+"/shares", APIPrefix+"/shares?kind=evaluation&target=ev_1"
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleMember} {
		// A kind or a target alone, or empty values, is the wide list too.
		for _, wide := range []string{APIPrefix + "/shares?kind=evaluation", APIPrefix + "/shares?target=ev_1", APIPrefix + "/shares?kind=&target=", APIPrefix + "/shares?kind=%20&target=ev_1"} {
			if res := shareCallAs(r, "GET", wide, nil, role); res.StatusCode() != 403 {
				t.Fatalf("%s %s: %d %s", role, wide, res.StatusCode(), res.Body())
			}
		}
		if res := shareCallAs(r, "GET", all, nil, role); res.StatusCode() != 403 || !strings.Contains(string(res.Body()), "Only an org owner or admin") {
			t.Fatalf("%s unfiltered: %d %s", role, res.StatusCode(), res.Body())
		}
	}
	for _, c := range []struct {
		role    auth.Role
		uri     string
		secrets bool
	}{
		{auth.RoleAdmin, all, true}, {auth.RoleOwner, all, true}, {auth.RoleAdmin, filtered, true},
		{auth.RoleMember, filtered, true}, {auth.RoleViewer, filtered, false},
	} {
		res := shareCallAs(r, "GET", c.uri, nil, c.role)
		l := d1Decode[ShareList](t, res)
		if res.StatusCode() != 200 || len(l.Shares) != 1 || l.Shares[0].ID == "" || l.Shares[0].State != "active" {
			t.Fatalf("%s %s: %d %s", c.role, c.uri, res.StatusCode(), res.Body())
		}
		v := l.Shares[0]
		if got := v.Token != "" && v.Path != ""; got != c.secrets {
			t.Fatalf("%s %s: secrets %v, want %v: %s", c.role, c.uri, got, c.secrets, res.Body())
		}
		if !c.secrets && (strings.Contains(string(res.Body()), `"token"`) || strings.Contains(string(res.Body()), `"path"`) || strings.Contains(string(res.Body()), `"url"`)) {
			t.Fatalf("viewer got a secret field: %s", res.Body())
		}
	}
}

// TestSharedCreatorName: the name is a member name only, never an email
// or an id.
func TestSharedCreatorName(t *testing.T) {
	prev := memberRealNames
	t.Cleanup(func() { SetMemberRealNames(prev) })
	var ctx fasthttp.RequestCtx
	if got := sharedCreatorName(&ctx, "usr_1"); got != "" {
		t.Fatalf("no org: %q", got)
	}
}

// TestCleanSharedName: the public creator name drops control and format
// characters and is absent when it looks like an email or an id, or is
// too long.
func TestCleanSharedName(t *testing.T) {
	for in, want := range map[string]string{
		"Asha":                        "Asha",
		"  Asha \t Rao ":              "Asha Rao",
		"Asha\u202eoaR":               "AshaoaR",
		"A\x1b[31mred":                "A[31mred",
		"asha@example.com":            "",
		"usr_123":                     "",
		"":                            "",
		strings.Repeat("a", 121):      "",
		"usr_actor_is_the_name_check": "",
	} {
		if got := cleanSharedName(in, "usr_x"); got != want {
			t.Fatalf("cleanSharedName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanSharedName("u_9", "u_9"); got != "" {
		t.Fatalf("the actor id itself: %q", got)
	}
}
