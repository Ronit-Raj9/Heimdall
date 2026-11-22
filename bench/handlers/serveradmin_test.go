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

func (fakeAdminRunners) RemoveRunnerRecord(context.Context, string, string) (*schemas.RunnerView, error) {
	return nil, schemas.ErrNotFound
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

// removableRunners is an AdminRunners with one offline and one live
// runner in org_a, recording removals.
type removableRunners struct {
	rows    map[string]schemas.RunnerView
	removed []string
}

func newRemovableRunners() *removableRunners {
	return &removableRunners{rows: map[string]schemas.RunnerView{
		"rnr_dead": {ID: "rnr_dead", OrgID: "org_a", Name: "demo-runner", Pool: "default", State: "lost", Slots: 1},
		"rnr_live": {ID: "rnr_live", OrgID: "org_a", Name: "shared-runner-org_a", Pool: "shared", State: "idle", Slots: 2, Live: true},
	}}
}

func (f *removableRunners) ListRunners(_ context.Context, org string) ([]schemas.RunnerView, error) {
	out := []schemas.RunnerView{}
	for _, id := range []string{"rnr_dead", "rnr_live"} {
		if r, ok := f.rows[id]; ok && r.OrgID == org {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *removableRunners) RemoveRunnerRecord(_ context.Context, org, id string) (*schemas.RunnerView, error) {
	r, ok := f.rows[id]
	if !ok || r.OrgID != org {
		return nil, schemas.ErrNotFound
	}
	delete(f.rows, id)
	f.removed = append(f.removed, org+"/"+id)
	return &r, nil
}

func removeRunnerServer(rs *removableRunners, actions *[]string) func(r *router.Router) {
	dir := &fakeAdminDir{orgs: []AdminOrg{{ID: "org_a", Slug: "a", Name: "Acme"}}}
	h := NewServerAdminHandler(schemas.ModeLive, dir, rs, &memQuota{m: map[string]quota.Limits{}}, true,
		func(_ *fasthttp.RequestCtx, org, action, _, id string, _ map[string]any) {
			*actions = append(*actions, action+" "+org+"/"+id)
		}).
		WithAdminCheck(func(p *auth.Principal) bool { return p != nil && p.Email == "root@x.test" })
	return func(r *router.Router) { h.RegisterRoutes(r) }
}

// TestAdminRemoveOfflineRunner (N11): a server admin removes an offline
// worker's record of any org; the removal is audited.
func TestAdminRemoveOfflineRunner(t *testing.T) {
	rs := newRemovableRunners()
	var actions []string
	reg := removeRunnerServer(rs, &actions)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_admin", Email: "root@x.test"}
	resp := call(t, reg, admin, "POST", APIPrefix+"/admin/containers/org_a/rnr_dead/remove", "")
	if resp.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("remove offline runner: %d %s", resp.StatusCode(), resp.Body())
	}
	var got schemas.RunnerView
	if err := json.Unmarshal(resp.Body(), &got); err != nil || got.Name != "demo-runner" {
		t.Fatalf("answer %s %v", resp.Body(), err)
	}
	if strings.Join(rs.removed, ",") != "org_a/rnr_dead" || strings.Join(actions, ",") != "admin.runner.remove org_a/rnr_dead" {
		t.Fatalf("removed %v audit %v", rs.removed, actions)
	}
	if resp := call(t, reg, admin, "POST", APIPrefix+"/admin/containers/org_a/rnr_nope/remove", ""); resp.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("unknown runner: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "GET", APIPrefix+"/admin/containers", "")
	var cs AdminContainerList
	_ = json.Unmarshal(resp.Body(), &cs)
	if len(cs.Runners) != 1 || cs.Runners[0].OrgName != "Acme" {
		t.Fatalf("containers after removal: %s", resp.Body())
	}
}

// TestAdminRemoveLiveRunnerRefused (N11): a worker that is online is not
// removed (409 with a plain sentence).
func TestAdminRemoveLiveRunnerRefused(t *testing.T) {
	rs := newRemovableRunners()
	var actions []string
	reg := removeRunnerServer(rs, &actions)
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_admin", Email: "root@x.test"}
	resp := call(t, reg, admin, "POST", APIPrefix+"/admin/containers/org_a/rnr_live/remove", "")
	if resp.StatusCode() != fasthttp.StatusConflict || !strings.Contains(string(resp.Body()), "This worker is online. Stop it or revoke its token first.") {
		t.Fatalf("remove live runner: %d %s", resp.StatusCode(), resp.Body())
	}
	if len(rs.removed) != 0 || len(actions) != 0 {
		t.Fatalf("removed %v audit %v", rs.removed, actions)
	}
}

// TestAdminRemoveRunnerNotAdmin (N11): only a server admin may remove a
// worker record.
func TestAdminRemoveRunnerNotAdmin(t *testing.T) {
	rs := newRemovableRunners()
	var actions []string
	reg := removeRunnerServer(rs, &actions)
	owner := &auth.Principal{Kind: auth.KindUser, UserID: "usr_b", Email: "b@x.test", Role: auth.RoleOwner}
	if resp := call(t, reg, owner, "POST", APIPrefix+"/admin/containers/org_a/rnr_dead/remove", ""); resp.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("non-admin remove: %d %s", resp.StatusCode(), resp.Body())
	}
	if len(rs.removed) != 0 {
		t.Fatalf("removed %v", rs.removed)
	}
}
