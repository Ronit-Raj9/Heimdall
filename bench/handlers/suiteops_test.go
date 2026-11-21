package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

func suiteOpsRoutes(src SuiteSource) func(*router.Router) {
	return func(r *router.Router) {
		h := &suiteOpsHandler{mode: schemas.ModeLive, suites: src}
		r.POST(APIPrefix+"/suites/{id}/tasks/{task}/quarantine", h.quarantine)
		r.POST(APIPrefix+"/suites/{id}/tasks/{task}/restore", h.restore)
	}
}

func TestSuiteOpsQuarantineRestore(t *testing.T) {
	dir := t.TempDir()
	s := &schemas.Suite{ID: "core", Version: "1.0.0", Root: dir, TaskIDs: []string{"t1"},
		Tasks: []*schemas.Task{{ID: "t1", Version: "2"}}}
	reg := suiteOpsRoutes(&fakeSuites{ids: []string{"core"}, suites: map[string]*schemas.Suite{"core": s}})
	base := APIPrefix + "/suites/core/tasks/"

	for _, tc := range []struct {
		name, uri, body string
		want            int
	}{
		{"no reason", base + "t1/quarantine", `{"reason":"  "}`, fasthttp.StatusBadRequest},
		{"unknown field", base + "t1/quarantine", `{"reason":"x","evil":1}`, fasthttp.StatusBadRequest},
		{"unknown task", base + "nope/quarantine", `{"reason":"x"}`, fasthttp.StatusNotFound},
		{"unknown suite", APIPrefix + "/suites/other/tasks/t1/quarantine", `{"reason":"x"}`, fasthttp.StatusNotFound},
		{"bad id", base + "..%2F..%2Fetc/quarantine", `{"reason":"x"}`, fasthttp.StatusNotFound},
	} {
		if got := call(t, reg, nil, "POST", tc.uri, tc.body).StatusCode(); got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, got, tc.want)
		}
	}

	resp := call(t, reg, nil, "POST", base+"t1/quarantine", `{"reason":"flaky verifier"}`)
	if resp.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("quarantine: %d %s", resp.StatusCode(), resp.Body())
	}
	var got QuarantineResult
	if err := json.Unmarshal(resp.Body(), &got); err != nil || !got.Changed || got.Quarantine == nil || got.Quarantine.Reason != "flaky verifier" ||
		got.Quarantine.Source != schemas.QuarantineManual || got.Quarantine.TaskVersion != "2" {
		t.Fatalf("quarantine result %+v (%v)", got, err)
	}
	f, err := suite.ReadHealthFile(dir)
	if err != nil || f.Quarantined["t1"].Reason != "flaky verifier" {
		t.Fatalf("sidecar %+v (%v)", f, err)
	}

	resp = call(t, reg, nil, "POST", base+"t1/restore", "")
	if err := json.Unmarshal(resp.Body(), &got); err != nil || resp.StatusCode() != fasthttp.StatusOK || !got.Changed {
		t.Fatalf("restore: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, nil, "POST", base+"t1/restore", "")
	if err := json.Unmarshal(resp.Body(), &got); err != nil || got.Changed {
		t.Fatalf("second restore should report no change: %s", resp.Body())
	}
	if f, _ := suite.ReadHealthFile(dir); len(f.Quarantined) != 0 {
		t.Fatalf("still quarantined: %+v", f)
	}
}

func TestSuiteOpsNoSuiteLoader(t *testing.T) {
	resp := call(t, suiteOpsRoutes(nil), nil, "POST", APIPrefix+"/suites/core/tasks/t1/restore", "")
	if resp.StatusCode() != fasthttp.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode())
	}
}
