package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/openmodel"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type fakeOpenModels struct {
	started []openmodel.StartRequest
	byOrg   map[string][]openmodel.Deployment
	err     error
}

func (f *fakeOpenModels) Catalog() openmodel.CatalogView { return openmodel.View() }
func (f *fakeOpenModels) List(_ context.Context, org string) ([]openmodel.Deployment, error) {
	return append([]openmodel.Deployment{}, f.byOrg[org]...), nil
}
func (f *fakeOpenModels) Get(_ context.Context, org, id string) (*openmodel.Deployment, error) {
	for _, d := range f.byOrg[org] {
		if d.ID == id {
			return &d, nil
		}
	}
	return nil, openmodel.ErrNotFound
}
func (f *fakeOpenModels) Start(_ context.Context, org, actor string, req openmodel.StartRequest) (*openmodel.Deployment, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.started = append(f.started, req)
	d := openmodel.Deployment{ID: "abcdefghij", OrgID: org, Model: req.Model, State: openmodel.StateStarting, CreatedBy: actor}
	f.byOrg[org] = append(f.byOrg[org], d)
	return &d, nil
}
func (f *fakeOpenModels) Stop(ctx context.Context, org, id, _ string) (*openmodel.Deployment, error) {
	d, err := f.Get(ctx, org, id)
	if err != nil {
		return nil, err
	}
	d.State = openmodel.StateStopped
	return d, nil
}
func (f *fakeOpenModels) OrphanApps(context.Context, string) ([]openmodel.AppInfo, error) {
	return []openmodel.AppInfo{}, nil
}

func TestOpenModelsRoutes(t *testing.T) {
	f := &fakeOpenModels{byOrg: map[string][]openmodel.Deployment{}}
	var audits []auditRow
	h := NewOpenModelsHandler(schemas.ModeLive, f, func(_ *fasthttp.RequestCtx, org, action, tt, id string, _ map[string]any) {
		audits = append(audits, auditRow{org, action, tt, id})
	})
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}
	viewer := &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleViewer}
	other := &auth.Principal{Kind: auth.KindUser, UserID: "usr_3", OrgID: "org_b", Role: auth.RoleAdmin}

	if resp := call(t, reg, nil, "GET", "/api/bench/open-models", ""); resp.StatusCode() != 401 {
		t.Fatalf("anonymous = %d", resp.StatusCode())
	}
	resp := call(t, reg, viewer, "GET", "/api/bench/open-models/catalog", "")
	var cat openmodel.CatalogView
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &cat) != nil || len(cat.Models) == 0 || len(cat.Engines) != 2 {
		t.Fatalf("catalog = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, viewer, "POST", "/api/bench/open-models", `{"model":"Qwen/Qwen2.5-0.5B-Instruct"}`); resp.StatusCode() != 403 {
		t.Fatalf("viewer start = %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/open-models", `{"model":"x","gpu_type":"L4"}`); resp.StatusCode() != 400 {
		t.Fatalf("unknown field = %d", resp.StatusCode())
	}
	resp = call(t, reg, admin, "POST", "/api/bench/open-models", `{"model":"Qwen/Qwen2.5-0.5B-Instruct","gpu":"L4","experiment_id":"exp-1","max_hours":1}`)
	if resp.StatusCode() != 202 || len(f.started) != 1 || f.started[0].ExpID != "exp-1" {
		t.Fatalf("start = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, viewer, "GET", "/api/bench/open-models/abcdefghij", ""); resp.StatusCode() != 200 {
		t.Fatalf("get = %d", resp.StatusCode())
	}
	if resp := call(t, reg, other, "GET", "/api/bench/open-models/abcdefghij", ""); resp.StatusCode() != 404 {
		t.Fatalf("other org get = %d", resp.StatusCode())
	}
	resp = call(t, reg, viewer, "GET", "/api/bench/open-models", "")
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), "abcdefghij") || string(resp.Header.Peek("Cache-Control")) != "no-store" {
		t.Fatalf("list = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, viewer, "POST", "/api/bench/open-models/abcdefghij/stop", ""); resp.StatusCode() != 403 {
		t.Fatalf("viewer stop = %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/open-models/abcdefghij/stop", ""); resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"state":"stopped"`) {
		t.Fatalf("stop = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "GET", "/api/bench/open-models/modal-apps", ""); resp.StatusCode() != 200 {
		t.Fatalf("apps = %d", resp.StatusCode())
	}
	f.err = openmodel.ErrNotConnected
	if resp := call(t, reg, admin, "POST", "/api/bench/open-models", `{"model":"Qwen/Qwen2.5-0.5B-Instruct"}`); resp.StatusCode() != 412 {
		t.Fatalf("not connected = %d", resp.StatusCode())
	}
	if len(audits) != 2 || audits[0].action != "open_model.start" || audits[1].action != "open_model.stop" {
		t.Fatalf("audits = %+v", audits)
	}
	off := NewOpenModelsHandler(schemas.ModeLive, nil, nil)
	if resp := call(t, func(r *router.Router) { off.RegisterRoutes(r) }, admin, "GET", "/api/bench/open-models", ""); resp.StatusCode() != 503 {
		t.Fatalf("local mode = %d", resp.StatusCode())
	}
}
