package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskcat"
	"github.com/valyala/fasthttp"
)

func TestCategoriesRoutes(t *testing.T) {
	dir := t.TempDir()
	s := &schemas.Suite{ID: "core", Version: "1.0.0", Root: dir, TaskIDs: []string{"t1", "t2", "t3"}, Tasks: []*schemas.Task{
		{ID: "t1", Title: "fix: crash on empty input"},
		{ID: "t2", Kind: schemas.TaskRefactor},
		{ID: "t3"},
	}}
	h := &taskCategoriesHandler{mode: schemas.ModeLive, suites: &fakeSuites{ids: []string{"core"}, suites: map[string]*schemas.Suite{"core": s}}}
	reg := func(r *router.Router) {
		r.GET(APIPrefix+"/suites/{id}/categories", h.get)
		r.POST(APIPrefix+"/suites/{id}/categories", h.backfill)
		r.PUT(APIPrefix+"/suites/{id}/tasks/{task}/category", h.set)
	}
	view := func(resp *fasthttp.Response) TaskCategoriesView {
		t.Helper()
		var v TaskCategoriesView
		if resp.StatusCode() != fasthttp.StatusOK || json.Unmarshal(resp.Body(), &v) != nil {
			t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
		}
		return v
	}
	if v := view(call(t, reg, nil, "GET", APIPrefix+"/suites/core/categories", "")); len(v.Labels) != 0 || v.Suite != "core" {
		t.Fatalf("empty = %+v", v)
	}
	if err := taskcat.Set(dir, "t2", taskcat.Label{Category: taskcat.Docs, Source: taskcat.SourcePerson}); err != nil {
		t.Fatal(err)
	}
	v := view(call(t, reg, nil, "POST", APIPrefix+"/suites/core/categories", ""))
	if v.Labelled != 1 || v.Labels["t1"].Category != taskcat.BugFix || v.Labels["t2"].Category != taskcat.Docs {
		t.Fatalf("backfill = %+v", v)
	}
	if _, ok := v.Labels["t3"]; ok {
		t.Fatalf("t3 labelled without a rule: %+v", v.Labels["t3"])
	}
	v = view(call(t, reg, nil, "PUT", APIPrefix+"/suites/core/tasks/t1/category", `{"category":"feature"}`))
	if v.Labels["t1"].Category != taskcat.Feature || v.Labels["t1"].Source != taskcat.SourcePerson {
		t.Fatalf("set = %+v", v.Labels["t1"])
	}
	for _, tc := range []struct {
		name, method, uri, body string
		want                    int
	}{
		{"bad category", "PUT", APIPrefix + "/suites/core/tasks/t1/category", `{"category":"other"}`, fasthttp.StatusBadRequest},
		{"unknown field", "PUT", APIPrefix + "/suites/core/tasks/t1/category", `{"category":"docs","x":1}`, fasthttp.StatusBadRequest},
		{"unknown task", "PUT", APIPrefix + "/suites/core/tasks/nope/category", `{"category":"docs"}`, fasthttp.StatusNotFound},
		{"unknown suite", "GET", APIPrefix + "/suites/other/categories", "", fasthttp.StatusNotFound},
	} {
		if got := call(t, reg, nil, tc.method, tc.uri, tc.body).StatusCode(); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}
}
