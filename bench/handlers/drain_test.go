package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/drain"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func decodeDrain(t *testing.T, resp *fasthttp.Response) drain.Status {
	t.Helper()
	var st drain.Status
	if err := json.Unmarshal(resp.Body(), &st); err != nil {
		t.Fatalf("decode %s: %v", resp.Body(), err)
	}
	return st
}

func TestDrainRoutesHosted(t *testing.T) {
	t.Cleanup(func() { drain.Stop(); drain.SetToken("") })
	drain.SetToken("tok-123")
	h := NewDrainHandler(schemas.ModeLive, true, nil, nil).
		WithAdminCheck(func(p *auth.Principal) bool { return p != nil && p.Email == "root@x.test" })
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	p := APIPrefix + "/admin/drain"
	user := &auth.Principal{Kind: auth.KindUser, UserID: "usr_b", Email: "b@x.test", Role: auth.RoleOwner}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_a", Email: "root@x.test"}

	if resp := call(t, reg, user, "POST", p, `{"on":true}`); resp.StatusCode() != fasthttp.StatusForbidden || drain.Active() {
		t.Fatalf("org owner drained the server: %d", resp.StatusCode())
	}
	if resp := call(t, reg, nil, "GET", p, ""); resp.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("anonymous read: %d", resp.StatusCode())
	}
	if resp := call(t, reg, nil, "POST", p, `{"on":true}`, drain.TokenHeader, "wrong"); resp.StatusCode() != fasthttp.StatusUnauthorized || drain.Active() {
		t.Fatalf("wrong token: %d", resp.StatusCode())
	}
	resp := call(t, reg, nil, "POST", p, `{"on":true}`, drain.TokenHeader, "tok-123")
	if st := decodeDrain(t, resp); resp.StatusCode() != 200 || !st.Draining || st.By != "drain token" || !drain.Active() {
		t.Fatalf("token drain: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "GET", p, "")
	if st := decodeDrain(t, resp); resp.StatusCode() != 200 || !st.Draining || !st.Drained {
		t.Fatalf("admin read: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", p, `{"on":true,"x":1}`); resp.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("unknown field: %d", resp.StatusCode())
	}
	resp = call(t, reg, admin, "POST", p, `{"on":false}`)
	if st := decodeDrain(t, resp); resp.StatusCode() != 200 || st.Draining || drain.Active() {
		t.Fatalf("admin resume: %d %s", resp.StatusCode(), resp.Body())
	}
}

func TestDrainRoutesLocal(t *testing.T) {
	t.Cleanup(func() { drain.Stop(); drain.SetToken("") })
	drain.SetToken("tok-123")
	h := NewDrainHandler(schemas.ModeLive, false, lib.RequireWriteToken(schemas.ModeLive, "write-tok"), nil)
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	p := APIPrefix + "/admin/drain"
	if resp := call(t, reg, nil, "GET", p, ""); resp.StatusCode() != 200 {
		t.Fatalf("local read: %d", resp.StatusCode())
	}
	if resp := call(t, reg, nil, "POST", p, `{"on":true}`); resp.StatusCode() != fasthttp.StatusUnauthorized || drain.Active() {
		t.Fatalf("local write without a token: %d", resp.StatusCode())
	}
	if resp := call(t, reg, nil, "POST", p, `{"on":true}`, "Authorization", "Bearer write-tok"); resp.StatusCode() != 200 || !drain.Active() {
		t.Fatalf("local write with the write token: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, nil, "POST", p, `{"on":false}`, drain.TokenHeader, "tok-123"); resp.StatusCode() != 200 || drain.Active() {
		t.Fatalf("local write with the drain token: %d %s", resp.StatusCode(), resp.Body())
	}
}
