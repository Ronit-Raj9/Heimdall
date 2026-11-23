package handlers

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// hw06Registry is a registry v2 API that signs in bot:good-token-1234.
func hw06Registry(t *testing.T) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); r.URL.Path == "/v2/" && ok && u == "bot" && p == "good-token-1234" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	c := srv.Client()
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server
	return strings.TrimPrefix(srv.URL, "https://"), c
}

func hw06Handler(t *testing.T) (func(*router.Router), *auth.Principal, string, *[]auditRow) {
	t.Helper()
	host, client := hw06Registry(t)
	conns := &sandbox.Connections{Keys: keystore.NewMemory(), RegistryClient: client,
		Build: func(string, sandbox.Credentials) (sandbox.Platform, error) { return okPlatform{}, nil }}
	audits := &[]auditRow{}
	h := NewWorkerPlatformsHandler(schemas.ModeLive, conns, func(_ *fasthttp.RequestCtx, org, action, tt, id string, _ map[string]any) {
		*audits = append(*audits, auditRow{org, action, tt, id})
	})
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/modal/connect", `{"token":"ak-1234567:as-12345678"}`); resp.StatusCode() != 200 {
		t.Fatalf("connect = %d %s", resp.StatusCode(), resp.Body())
	}
	return reg, admin, host, audits
}

// Fix wave 3 (N216 fix 2): POST /worker-platforms/{platform}/registry signs
// in to the registry before it keeps the login.
func TestRegistryLoginSaveRoute(t *testing.T) {
	reg, admin, host, audits := hw06Handler(t)
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleMember}
	path := "/api/bench/worker-platforms/modal/registry"
	if resp := call(t, reg, member, "POST", path, `{}`); resp.StatusCode() != 403 {
		t.Fatalf("member = %d", resp.StatusCode())
	}
	resp := call(t, reg, admin, "POST", path, `{"registry":"heimdall","username":"bot","token":"good-token-1234"}`)
	if resp.StatusCode() != 400 || !strings.Contains(string(resp.Body()), "Use a registry path like ghcr.io/team/heimdall.") {
		t.Fatalf("bad path = %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "POST", path, `{"registry":"`+host+`/team/heimdall","username":"bot","token":"wrong-token-9999"}`)
	if resp.StatusCode() != 422 || !strings.Contains(string(resp.Body()), "Heimdall could not sign in to "+host+"/team/heimdall: the user name or token was refused.") ||
		strings.Contains(string(resp.Body()), "wrong-token") {
		t.Fatalf("refused = %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "POST", path, `{"registry":"`+host+`/team/heimdall","username":"bot","token":"good-token-1234"}`)
	var v sandbox.RegistryView
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &v) != nil || v.Registry != host+"/team/heimdall" || v.TokenLast4 != "1234" || v.TestedAt == nil {
		t.Fatalf("save = %d %s", resp.StatusCode(), resp.Body())
	}
	if len(*audits) != 2 || (*audits)[1].action != "worker_platform.registry" {
		t.Fatalf("audits = %+v", *audits)
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/e2b/registry", `{"registry":"`+host+`/team/heimdall","username":"bot","token":"good-token-1234"}`); resp.StatusCode() != 400 {
		t.Fatalf("registry for a platform that does not take one = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", path+"/remove", ""); resp.StatusCode() != 204 {
		t.Fatalf("remove = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", path+"/remove", ""); resp.StatusCode() != 404 {
		t.Fatalf("remove again = %d", resp.StatusCode())
	}
}

// The token goes in, never out: not in the save answer, the list, or an
// error.
func TestRegistryLoginNeverReturnsToken(t *testing.T) {
	reg, admin, host, _ := hw06Handler(t)
	body := `{"registry":"` + host + `/team/heimdall","username":"bot","token":"good-token-1234"}`
	for _, resp := range []*fasthttp.Response{
		call(t, reg, admin, "POST", "/api/bench/worker-platforms/modal/registry", body),
		call(t, reg, admin, "GET", "/api/bench/worker-platforms", ""),
	} {
		if resp.StatusCode() != 200 || strings.Contains(string(resp.Body()), "good-token") {
			t.Fatalf("answer = %d %s", resp.StatusCode(), resp.Body())
		}
	}
	resp := call(t, reg, admin, "GET", "/api/bench/worker-platforms", "")
	if !strings.Contains(string(resp.Body()), `"registry":{"registry":"`+host+`/team/heimdall","username":"bot","token_last4":"1234"`) {
		t.Fatalf("list = %s", resp.Body())
	}
}
