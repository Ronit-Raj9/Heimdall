package handlers

import (
	"encoding/json"
	"testing"

	"github.com/valyala/fasthttp"
)

// TestAppTasksWriteQueuesReadiness: after tasks from an API are written, the
// readiness check of their benchmark is queued, so the task quality guard
// runs; without a jobs service nothing is queued.
func TestAppTasksWriteQueuesReadiness(t *testing.T) {
	fj := &fakeReadinessJobs{}
	h := &appTasksArea{suiteAuthoring: &suiteAuthoring{}, jobs: fj, org: "org_1"}
	id := h.queueReadiness(&fasthttp.RequestCtx{}, "shop-support")
	if id == "" || len(fj.created) != 1 {
		t.Fatalf("queued %q, jobs %+v", id, fj.created)
	}
	n := fj.created[0]
	var p map[string]string
	_ = json.Unmarshal(n.Params, &p)
	if n.Kind != readinessJobKind || n.OrgID != "org_1" || p["suite"] != "shop-support" {
		t.Fatalf("job = %+v params %v", n, p)
	}
	none := &appTasksArea{suiteAuthoring: &suiteAuthoring{}}
	if id := none.queueReadiness(&fasthttp.RequestCtx{}, "shop-support"); id != "" {
		t.Fatalf("no jobs service queued %q", id)
	}
}
