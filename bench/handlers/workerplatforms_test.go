package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type okPlatform struct {
	sandbox.Platform
	err error
}

func (p okPlatform) Check(context.Context) error { return p.err }

func TestWorkerPlatformsRoutes(t *testing.T) {
	var checkErr error
	conns := &sandbox.Connections{Keys: keystore.NewMemory(), Build: func(string, sandbox.Credentials) (sandbox.Platform, error) {
		return okPlatform{err: checkErr}, nil
	}}
	var audits []auditRow
	h := NewWorkerPlatformsHandler(schemas.ModeLive, conns, func(_ *fasthttp.RequestCtx, org, action, tt, id string, _ map[string]any) {
		audits = append(audits, auditRow{org, action, tt, id})
	})
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleMember}

	if resp := call(t, reg, nil, "GET", "/api/bench/worker-platforms", ""); resp.StatusCode() != 401 {
		t.Fatalf("anonymous = %d", resp.StatusCode())
	}
	if resp := call(t, reg, member, "POST", "/api/bench/worker-platforms/e2b/connect", `{"token":"e2b_x"}`); resp.StatusCode() != 403 {
		t.Fatalf("member = %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/fly/connect", `{"token":"x"}`); resp.StatusCode() != 404 {
		t.Fatalf("unknown platform = %d", resp.StatusCode())
	}
	checkErr = fmt.Errorf("%w (HTTP 401)", sandbox.ErrUnauthorized)
	resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/e2b/connect", `{"token":"e2b_refused_token_1"}`)
	if resp.StatusCode() != 422 || strings.Contains(string(resp.Body()), "e2b_refused_token_1") {
		t.Fatalf("refused token = %d %s", resp.StatusCode(), resp.Body())
	}
	checkErr = nil
	resp = call(t, reg, admin, "POST", "/api/bench/worker-platforms/e2b/connect", `{"token":"e2b_good_token_9876"}`)
	if resp.StatusCode() != 200 || strings.Contains(string(resp.Body()), "e2b_good_token_9876") || !strings.Contains(string(resp.Body()), `"token_last4":"9876"`) {
		t.Fatalf("connect = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/e2b/connect", `{"token":"x","extra":1}`); resp.StatusCode() != 400 {
		t.Fatalf("unknown field = %d", resp.StatusCode())
	}
	resp = call(t, reg, admin, "POST", "/api/bench/worker-platforms/default", `{"platform":"e2b"}`)
	var l sandbox.ConnectionsList
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &l) != nil || l.Default != "e2b" {
		t.Fatalf("default = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/default", `{"platform":"daytona"}`); resp.StatusCode() != 404 {
		t.Fatalf("default of an unconnected platform = %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/e2b/test", ""); resp.StatusCode() != 200 {
		t.Fatalf("test = %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "GET", "/api/bench/worker-platforms", "")
	if resp.StatusCode() != 200 || string(resp.Header.Peek("Cache-Control")) != "no-store" {
		t.Fatalf("list = %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/worker-platforms/e2b/disconnect", ""); resp.StatusCode() != 200 {
		t.Fatalf("disconnect = %d", resp.StatusCode())
	}
	want := []string{"worker_platform.connect", "worker_platform.default", "worker_platform.disconnect"}
	if len(audits) != len(want) {
		t.Fatalf("audits = %+v", audits)
	}
	for i, a := range audits {
		if a.action != want[i] || a.org != "org_a" || a.targetType != "worker_platform" {
			t.Fatalf("audit %d = %+v", i, a)
		}
	}
	off := NewWorkerPlatformsHandler(schemas.ModeLive, nil, nil)
	if resp := call(t, func(r *router.Router) { off.RegisterRoutes(r) }, admin, "GET", "/api/bench/worker-platforms", ""); resp.StatusCode() != 503 {
		t.Fatalf("off = %d", resp.StatusCode())
	}
}
