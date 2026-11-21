package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type fakeAdminDir struct {
	users []AdminUser
	orgs  []AdminOrg
}

func (f *fakeAdminDir) Users(context.Context) ([]AdminUser, error) { return f.users, nil }
func (f *fakeAdminDir) Orgs(context.Context) ([]AdminOrg, error) {
	return append([]AdminOrg(nil), f.orgs...), nil
}
func (f *fakeAdminDir) SetUserDisabled(_ context.Context, id string, d bool) error {
	for i := range f.users {
		if f.users[i].ID == id {
			f.users[i].Disabled = d
			return nil
		}
	}
	return ErrAdminNotFound
}

type fakeAdminRunners struct{}

func (fakeAdminRunners) ListRunners(_ context.Context, org string) ([]schemas.RunnerView, error) {
	if org != "org_a" {
		return nil, nil
	}
	return []schemas.RunnerView{{ID: "rnr_1", Name: "shared-1", Pool: "shared", State: "running", Running: []string{"s1"}, RunningSlots: 1, Slots: 2, Live: true}}, nil
}

type memQuota struct {
	mu   sync.Mutex
	m    map[string]quota.Limits
	kill bool
}

func (q *memQuota) Get(_ context.Context, org string) (quota.Limits, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if l, ok := q.m[org]; ok {
		return l, nil
	}
	return quota.DefaultLimits, nil
}
func (q *memQuota) Set(_ context.Context, org string, l quota.Limits, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.m[org] = l
	return nil
}
func (q *memQuota) Defaults() quota.Limits                   { return quota.DefaultLimits }
func (q *memQuota) KillSwitch(context.Context) (bool, error) { return q.kill, nil }
func (q *memQuota) SetKillSwitch(_ context.Context, on bool, _ string) error {
	q.kill = on
	return nil
}

func TestServerAdminRoutes(t *testing.T) {
	dir := &fakeAdminDir{
		users: []AdminUser{{ID: "usr_admin", Email: "root@x.test"}, {ID: "usr_b", Email: "b@x.test"}},
		orgs:  []AdminOrg{{ID: "org_a", Slug: "a"}, {ID: "org_b", Slug: "b"}},
	}
	q := &memQuota{m: map[string]quota.Limits{}}
	var actions []string
	h := NewServerAdminHandler(schemas.ModeLive, dir, fakeAdminRunners{}, q, true,
		func(_ *fasthttp.RequestCtx, _, action, _, _ string, _ map[string]any) {
			actions = append(actions, action)
		}).
		WithAdminCheck(func(p *auth.Principal) bool { return p != nil && p.Email == "root@x.test" })
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_admin", Email: "root@x.test"}
	user := &auth.Principal{Kind: auth.KindUser, UserID: "usr_b", Email: "b@x.test", Role: auth.RoleOwner}

	for _, path := range []string{"/users", "/orgs", "/containers", "/killswitch", "/overview", "/orgs/org_a/quota"} {
		if resp := call(t, reg, user, "GET", APIPrefix+"/admin"+path, ""); resp.StatusCode() != fasthttp.StatusForbidden {
			t.Fatalf("non-admin GET %s: %d, want 403", path, resp.StatusCode())
		}
	}
	if resp := call(t, reg, user, "POST", APIPrefix+"/admin/killswitch", `{"on":true}`); resp.StatusCode() != fasthttp.StatusForbidden || q.kill {
		t.Fatalf("non-admin kill switch: %d", resp.StatusCode())
	}

	resp := call(t, reg, admin, "POST", APIPrefix+"/admin/orgs/org_a/quota",
		`{"max_concurrent_runs":5,"max_runs_per_day":10,"max_spend_usd_per_day":3.5,"max_image_bytes":1024,"allow_custom_images":true,"shared_pool":true,"runtime":"runsc"}`)
	if resp.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("set quota: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "GET", APIPrefix+"/admin/orgs/org_a/quota", "")
	var got AdminQuota
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Limits.MaxConcurrentRuns != 5 || got.Limits.Runtime != "runsc" || !got.Limits.SharedPool || !got.Persisted {
		t.Fatalf("quota did not persist: %+v", got)
	}
	if resp := call(t, reg, admin, "POST", APIPrefix+"/admin/orgs/org_a/quota", `{"max_concurrent_runs":-1,"runtime":"kata"}`); resp.StatusCode() != fasthttp.StatusBadRequest ||
		!strings.Contains(string(resp.Body()), "runtime") {
		t.Fatalf("bad quota: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "GET", APIPrefix+"/admin/orgs/org_zzz/quota", ""); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("unknown org quota: %d", resp.StatusCode())
	}

	resp = call(t, reg, admin, "GET", APIPrefix+"/admin/orgs", "")
	var orgs AdminOrgList
	_ = json.Unmarshal(resp.Body(), &orgs)
	if len(orgs.Orgs) != 2 || orgs.Orgs[0].Quota == nil || orgs.Orgs[0].Quota.MaxConcurrentRuns != 5 {
		t.Fatalf("orgs: %s", resp.Body())
	}

	if resp := call(t, reg, admin, "POST", APIPrefix+"/admin/killswitch", `{"on":true}`); resp.StatusCode() != fasthttp.StatusOK || !q.kill {
		t.Fatalf("kill switch: %d %s", resp.StatusCode(), resp.Body())
	}

	if resp := call(t, reg, admin, "POST", APIPrefix+"/admin/users/usr_admin/disable", `{"disabled":true}`); resp.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("self disable: %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", APIPrefix+"/admin/users/usr_b/disable", `{"disabled":true}`); resp.StatusCode() != fasthttp.StatusOK || !dir.users[1].Disabled {
		t.Fatalf("disable: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", APIPrefix+"/admin/users/usr_nope/disable", `{"disabled":true}`); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("unknown user: %d", resp.StatusCode())
	}

	resp = call(t, reg, admin, "GET", APIPrefix+"/admin/containers", "")
	var cs AdminContainerList
	_ = json.Unmarshal(resp.Body(), &cs)
	if len(cs.Runners) != 1 || cs.RunningSlots != 1 || cs.Runners[0].OrgSlug != "a" {
		t.Fatalf("containers: %s", resp.Body())
	}
	want := "admin.quota.set,admin.killswitch.set,admin.user.disable"
	if strings.Join(actions, ",") != want {
		t.Fatalf("audit %v, want %s", actions, want)
	}
}

func TestServerAdminNilDeps(t *testing.T) {
	h := NewServerAdminHandler(schemas.ModeLive, nil, nil, nil, false, nil).WithAdminCheck(func(*auth.Principal) bool { return true })
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	p := &auth.Principal{Kind: auth.KindUser, UserID: "u"}
	for _, path := range []string{"/users", "/orgs", "/containers", "/killswitch"} {
		if resp := call(t, reg, p, "GET", APIPrefix+"/admin"+path, ""); resp.StatusCode() != fasthttp.StatusServiceUnavailable {
			t.Fatalf("GET %s: %d, want 503", path, resp.StatusCode())
		}
	}
}
