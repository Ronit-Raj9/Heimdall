package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/valyala/fasthttp"
)

func TestRegistryCredentialsCRUD(t *testing.T) {
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Mode: schemas.ModeLive, Services: Services{OrgID: "o1", Keys: ks}}
	h := &registryCredsHandler{s: &secretsHandler{env: env}}
	r := router.New()
	r.GET(RegistryCredentialsPath, env.R(h.list))
	r.POST(RegistryCredentialsPath, env.W(h.create))
	r.DELETE(RegistryCredentialsPath+"/{id}", env.W(h.remove))
	_, _ = ks.Create(context.Background(), "o1", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "GHCR_TOKEN", Value: "ghp_topsecret"}, "u")

	st, body := secretsCall(r.Handler, "POST", RegistryCredentialsPath, `{"kind":"ghcr","username":"octo","secret_name":"GHCR_TOKEN"}`)
	if st != fasthttp.StatusCreated || !strings.Contains(body, `"host":"ghcr.io"`) || !strings.Contains(body, `"secret_found":true`) {
		t.Fatalf("create: %d %s", st, body)
	}
	var c RegistryCredential
	_ = json.Unmarshal([]byte(body), &c)
	if st, _ := secretsCall(r.Handler, "POST", RegistryCredentialsPath, `{"kind":"ghcr","username":"x","secret_name":"GHCR_TOKEN"}`); st != fasthttp.StatusConflict {
		t.Errorf("duplicate host: %d", st)
	}
	if st, _ := secretsCall(r.Handler, "POST", RegistryCredentialsPath, `{"kind":"ecr","host":"bad","username":"x","secret_name":"A"}`); st != fasthttp.StatusBadRequest {
		t.Errorf("bad ecr: %d", st)
	}
	st, body = secretsCall(r.Handler, "GET", RegistryCredentialsPath, "")
	if st != 200 || !strings.Contains(body, "octo") || strings.Contains(body, "ghp_topsecret") {
		t.Fatalf("list: %d %s", st, body)
	}
	// Registry logins are not secrets: the secrets list stays as it was.
	sh := secretsRouter(t, ks)
	if _, b := secretsCall(sh, "GET", APIPrefix+"/secrets", ""); strings.Contains(b, "ghcr.io") {
		t.Fatalf("secrets list shows a registry login: %s", b)
	}
	if st, _ := secretsCall(r.Handler, "DELETE", RegistryCredentialsPath+"/"+c.ID, ""); st != 200 {
		t.Fatalf("delete: %d", st)
	}
	if st, _ := secretsCall(r.Handler, "DELETE", RegistryCredentialsPath+"/"+c.ID, ""); st != 404 {
		t.Fatalf("second delete: %d", st)
	}
}

func TestLeaseCredentialsRegistries(t *testing.T) {
	ks := credsStore(t)
	ctx := context.Background()
	_, _ = ks.Create(ctx, "org_a", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "GHCR_TOKEN", Value: "ghp_reg"}, "u")
	_, _ = ks.Create(ctx, "org_a", keystore.KindRegistry, keystore.Put{Provider: "ghcr", Label: "ghcr.io", Value: `{"username":"octo","secret_name":"GHCR_TOKEN"}`}, "u")
	holders := fakeHolders{"l1": {WorkerID: "w1", TokenID: "rtk_a1", OrgID: "org_a"}}
	h := &leaseCredsHandler{mode: schemas.ModeLive, orgID: "org_a", keys: ks, holders: func(string) LeaseHolders { return holders }, now: time.Now}
	c := callCreds(h, &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a", TokenID: "rtk_a1"}, "l1")
	var out schemas.LeaseCredentials
	if err := json.Unmarshal(c.Response.Body(), &out); err != nil || len(out.Registries) != 1 || out.Registries[0].Password != "ghp_reg" || out.Registries[0].Username != "octo" {
		t.Fatalf("registries %d %s", c.Response.StatusCode(), c.Response.Body())
	}
}

func TestImageAutoApproveRunsc(t *testing.T) {
	d := dbtest.OpenSQLite(t)
	def := quota.DefaultLimits
	def.SharedPool = true
	def.Runtime = quota.RuntimeRunsc
	q := quota.Open(d, quota.Options{Defaults: &def})
	images := &sharedpool.ImageStore{DB: d}
	svc := &sharedpool.Service{Quota: q, Minter: spMinter{}, Images: images, Orgs: func(context.Context) ([]string, error) { return []string{"org1"}, nil }}
	h := NewSharedPoolHandler(schemas.ModeLive, q, images, svc, nil).WithSecret(func() string { return "s" })
	member := &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleMember, Email: "m@x.io"}
	if code, body := spServe(t, h, member, "GET", "/api/bench/pool", "", ""); code != 200 || !strings.Contains(body, `"auto_approve_images":true`) {
		t.Fatalf("pool: %d %s", code, body)
	}
	code, body := spServe(t, h, member, "POST", "/api/bench/images", `{"image":"ghcr.io/a/b@sha256:`+strings.Repeat("c", 64)+`","size_bytes":10}`, "")
	if code != 201 || !strings.Contains(body, `"status":"approved"`) || !strings.Contains(body, `"approval":"automatic"`) || !strings.Contains(body, "next_step") {
		t.Fatalf("request: %d %s", code, body)
	}
	if l, _ := q.Get(context.Background(), "org1"); !l.AllowCustomImages {
		t.Fatal("automatic approval did not allow custom images")
	}
}
