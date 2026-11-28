package handlers

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/statuspage"
	"github.com/valyala/fasthttp"
)

func statusIncServe(t *testing.T, h *StatusPageHandler, p *auth.Principal, path, body string) (int, string) {
	t.Helper()
	r := router.New()
	h.RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(path)
	ctx.Request.SetBodyString(body)
	if p != nil {
		auth.SetPrincipal(ctx, p)
	}
	r.Handler(ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

// The bodies are the ones the status page's incident form sends
// (ui/app/status/views/incidentForm.tsx, incidentBody / noteBody).
func TestStatusIncidentsFromWebForm(t *testing.T) {
	store := statuspage.NewStore(filepath.Join(t.TempDir(), "incidents.json"))
	h := NewStatusPageHandler(schemas.ModeLive, statuspage.New(nil, store, "test"))
	root := &auth.Principal{Kind: auth.KindUser, UserID: "u1", Email: "root@h.test"}
	other := &auth.Principal{Kind: auth.KindUser, UserID: "u2", Email: "x@h.test", Role: auth.RoleOwner, OrgID: "o1"}
	prev := statusIsAdmin
	statusIsAdmin = func(p *auth.Principal) bool { return p != nil && p.Email == "root@h.test" }
	defer func() { statusIsAdmin = prev }()

	open := `{"title":"Runs start late","message":"Looking into it.","impact":"major"}`
	if code, body := statusIncServe(t, h, other, StatusIncidentsPath, open); code != 403 || !strings.Contains(body, "server admins only") {
		t.Fatalf("non admin: %d %s", code, body)
	}
	code, body := statusIncServe(t, h, root, StatusIncidentsPath, open)
	if code != 200 {
		t.Fatalf("open: %d %s", code, body)
	}
	var in statuspage.Incident
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	if in.ID == "" || in.Impact != "major" || in.Status != "investigating" {
		t.Fatalf("open: %+v", in)
	}
	if code, body := statusIncServe(t, h, root, StatusIncidentsPath, `{"title":"x","message":"y","impact":"huge"}`); code != 400 || !strings.Contains(body, "impact") {
		t.Fatalf("bad impact: %d %s", code, body)
	}
	if code, body := statusIncServe(t, h, root, StatusIncidentsPath+"/nope", `{"message":"m"}`); code != 404 {
		t.Fatalf("unknown id: %d %s", code, body)
	}
	if code, body := statusIncServe(t, h, root, StatusIncidentsPath+"/"+in.ID, `{"message":"Fixed.","status":"resolved"}`); code != 200 || !strings.Contains(body, "resolved_at") {
		t.Fatalf("resolve: %d %s", code, body)
	}
	list, err := store.List()
	if err != nil || len(list) != 1 || list[0].Status != "resolved" {
		t.Fatalf("store: %v %+v", err, list)
	}
}
