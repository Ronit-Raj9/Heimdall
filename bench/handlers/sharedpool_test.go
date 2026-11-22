package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/valyala/fasthttp"
)

type spMinter struct{}

func (spMinter) CreateToken(_ context.Context, org, _ string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error) {
	return &schemas.RunnerTokenCreated{Token: "hmr_x_" + org, Info: schemas.RunnerTokenView{ID: "rtk1", Pool: req.Pool}}, nil
}
func (spMinter) ListTokens(context.Context, string) ([]schemas.RunnerTokenView, error) {
	return nil, nil
}
func (spMinter) RevokeToken(context.Context, string, string) (*schemas.RunnerTokenView, error) {
	return nil, nil
}

func spServe(t *testing.T, h *SharedPoolHandler, p *auth.Principal, method, path, body, authz string) (int, string) {
	t.Helper()
	r := router.New()
	h.RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	if body != "" {
		ctx.Request.SetBodyString(body)
	}
	if authz != "" {
		ctx.Request.Header.Set("Authorization", authz)
	}
	if p != nil {
		auth.SetPrincipal(ctx, p)
	}
	r.Handler(ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

func TestSharedPoolRoutes(t *testing.T) {
	d := dbtest.OpenSQLite(t)
	def := quota.DefaultLimits
	def.SharedPool = true
	def.Runtime = "runc" // the manual approval path (J6); gVisor orgs get digest-pinned images approved automatically (Y10)
	q := quota.Open(d, quota.Options{Defaults: &def})
	keys, _ := keystore.Open(d, make([]byte, 32))
	images := &sharedpool.ImageStore{DB: d}
	svc := &sharedpool.Service{Quota: q, Keys: keys, Minter: spMinter{}, Images: images,
		Orgs: func(context.Context) ([]string, error) { return []string{"org1"}, nil }}
	h := NewSharedPoolHandler(schemas.ModeLive, q, images, svc, nil).WithSecret(func() string { return "s3cret" })
	member := &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleMember, Email: "m@x.io"}
	viewer := &auth.Principal{Kind: auth.KindUser, UserID: "u2", OrgID: "org1", Role: auth.RoleViewer, Email: "v@x.io"}

	if code, body := spServe(t, h, member, "POST", "/api/bench/images", `{"image":"python:3.12","size_bytes":10}`, ""); code != 400 || !strings.Contains(body, "digest") {
		t.Fatalf("unpinned image: %d %s", code, body)
	}
	pinned := `{"image":"python@sha256:` + strings.Repeat("b", 64) + `","size_bytes":%d}`
	if code, _ := spServe(t, h, member, "POST", "/api/bench/images", strings.Replace(pinned, "%d", "99999999999", 1), ""); code != 413 {
		t.Fatalf("oversize image: %d", code)
	}
	if code, _ := spServe(t, h, viewer, "POST", "/api/bench/images", strings.Replace(pinned, "%d", "100", 1), ""); code != 403 {
		t.Fatalf("viewer wrote: %d", code)
	}
	if code, body := spServe(t, h, member, "POST", "/api/bench/images", strings.Replace(pinned, "%d", "100", 1), ""); code != 201 {
		t.Fatalf("request: %d %s", code, body)
	}
	if code, body := spServe(t, h, viewer, "GET", "/api/bench/images", "", ""); code != 200 || !strings.Contains(body, "pending") {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, _ := spServe(t, h, member, "POST", "/api/bench/admin/images/x/approve", `{}`, ""); code != 403 {
		t.Fatalf("non admin approve: %d", code)
	}
	// J6: a server admin approves; approval lets the org run custom images.
	var reqs ImageRequestList
	_, body := spServe(t, h, viewer, "GET", "/api/bench/images", "", "")
	_ = json.Unmarshal([]byte(body), &reqs)
	if l, _ := q.Get(context.Background(), "org1"); l.AllowCustomImages {
		t.Fatal("custom images allowed before approval")
	}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "a1", OrgID: "org1", Role: auth.RoleMember, Email: "admin@x.io"}
	ha := NewSharedPoolHandler(schemas.ModeLive, q, images, svc, nil).WithAdminCheck(func(p *auth.Principal) bool { return p != nil && p.Email == "admin@x.io" })
	if code, body := spServe(t, ha, admin, "POST", "/api/bench/admin/images/"+reqs.Images[0].ID+"/approve", `{"note":"ok"}`, ""); code != 200 || !strings.Contains(body, "approved") {
		t.Fatalf("approve: %d %s", code, body)
	}
	if l, _ := q.Get(context.Background(), "org1"); !l.AllowCustomImages {
		t.Fatal("approval did not allow custom images")
	}
	if code, _ := spServe(t, ha, member, "POST", "/api/bench/admin/images/"+reqs.Images[0].ID+"/reject", `{}`, ""); code != 403 {
		t.Fatalf("member reject: %d", code)
	}
	if code, _ := spServe(t, h, nil, "GET", "/api/bench/admin/pool/assignments", "", "Bearer wrong"); code != 401 {
		t.Fatalf("bad secret: %d", code)
	}
	code, body := spServe(t, h, nil, "GET", "/api/bench/admin/pool/assignments", "", "Bearer s3cret")
	_ = body
	var as sharedpool.Assignments
	if code != 200 || json.Unmarshal([]byte(body), &as) != nil || len(as.Assignments) != 1 || strings.Contains(body, "hmr_") {
		t.Fatalf("assignments (must hold no token): %d %s", code, body)
	}
	if code, _ := spServe(t, h, nil, "POST", "/api/bench/admin/pool/assignments/org1/token", "", "Bearer wrong"); code != 401 {
		t.Fatalf("claim with bad secret: %d", code)
	}
	code, body = spServe(t, h, nil, "POST", "/api/bench/admin/pool/assignments/org1/token", "", "Bearer s3cret")
	var claim sharedpool.ClaimedToken
	if code != 200 || json.Unmarshal([]byte(body), &claim) != nil || claim.Token != "hmr_x_org1" || claim.OrgID != "org1" {
		t.Fatalf("claim: %d %s", code, body)
	}
	_ = q.Set(context.Background(), "org9", quota.Limits{}, "t")
	if code, _ := spServe(t, h, nil, "POST", "/api/bench/admin/pool/assignments/org9/token", "", "Bearer s3cret"); code != 404 {
		t.Fatalf("claim for an org off the pool: %d", code)
	}
	_ = q.SetKillSwitch(context.Background(), true, "t")
	_, body = spServe(t, h, nil, "GET", "/api/bench/admin/pool/assignments", "", "Bearer s3cret")
	if !strings.Contains(body, `"kill_switch":true`) || !strings.Contains(body, `"assignments":[]`) {
		t.Fatalf("kill switch assignments: %s", body)
	}
	if code, body := spServe(t, h, viewer, "GET", "/api/bench/pool", "", ""); code != 200 || !strings.Contains(body, "kill switch") {
		t.Fatalf("status: %d %s", code, body)
	}
}
