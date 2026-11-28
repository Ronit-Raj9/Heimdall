package handlers

import (
	"encoding/json"
	"testing"
)

// TestLiveMonitorEditFromWebBody sends the body the Edit form builds
// (ui/app/scheduled-checks/views/editLiveMonitor.ufl8.test.ts) through PUT.
func TestLiveMonitorEditFromWebBody(t *testing.T) {
	w := newMonitorWorld(t)
	d := createMonitor(t, w, `{"agent":"support-bot","benchmark":"svc-bench"}`)
	ctx := evalCall(w.r, "PUT", LiveMonitorsPath+"/"+d.ID, `{"interval_min":30,"rule":{"window":6,"min_drop":0.2,"min_pass_rate":0.5}}`)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("update: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var u MonitorDetail
	if err := json.Unmarshal(ctx.Response.Body(), &u); err != nil {
		t.Fatal(err)
	}
	if u.IntervalMin != 30 || u.Rule.MinDrop != 0.2 || u.Rule.MinPassRate != 0.5 || u.Rule.Window != 6 || u.Name != d.Name {
		t.Fatalf("updated: %+v", u.MonitorRow)
	}
	bad := evalCall(w.r, "PUT", LiveMonitorsPath+"/"+d.ID, `{"interval_min":1}`)
	if bad.Response.StatusCode() != 422 {
		t.Fatalf("too often accepted: %d", bad.Response.StatusCode())
	}
}
