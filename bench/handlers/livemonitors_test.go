package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/livemon"
	"github.com/maximhq/bifrost/bench/schedule"
)

func createMonitor(t *testing.T, w *monitorWorld, body string) MonitorDetail {
	t.Helper()
	ctx := evalCall(w.r, "POST", LiveMonitorsPath, body)
	if ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var d MonitorDetail
	if err := json.Unmarshal(ctx.Response.Body(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLiveMonitorCreateValidates(t *testing.T) {
	w := newMonitorWorld(t)
	tests := []struct {
		name, body, want string
		status           int
	}{
		{"unknown agent", `{"agent":"nobody","benchmark":"svc-bench"}`, "does not exist", 422},
		{"unknown benchmark", `{"agent":"support-bot","benchmark":"nope"}`, "does not exist on this server", 422},
		{"too often", `{"agent":"support-bot","benchmark":"svc-bench","interval_min":5}`, "interval_min", 422},
		{"too many tasks", `{"agent":"support-bot","benchmark":"svc-bench","tasks":9}`, "1 to 5 tasks", 422},
		{"no benchmark and no evaluation", `{"agent":"support-bot"}`, "pick a benchmark", 422},
		{"unknown field", `{"agent":"support-bot","bogus":1}`, "bogus", 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := evalCall(w.r, "POST", LiveMonitorsPath, tc.body)
			if ctx.Response.StatusCode() != tc.status || !strings.Contains(string(ctx.Response.Body()), tc.want) {
				t.Fatalf("%d %s, want %d %q", ctx.Response.StatusCode(), ctx.Response.Body(), tc.status, tc.want)
			}
		})
	}
}

func TestLiveMonitorDefaultsAndLifecycle(t *testing.T) {
	w := newMonitorWorld(t)
	d := createMonitor(t, w, `{"agent":"support-bot","benchmark":"svc-bench"}`)
	if d.Tasks == nil || len(d.Tasks) != livemon.DefaultTasks || d.IntervalMin != 60 || d.BudgetUSDPerDay != 6 || d.CheckBudgetUSD != 0.25 {
		t.Fatalf("defaults: %+v", d.MonitorRow)
	}
	if d.Next == "" || d.Problem != "" || d.Schedule != "live-"+d.ID {
		t.Fatalf("schedule: next %q problem %q schedule %q", d.Next, d.Problem, d.Schedule)
	}
	// The schedule extras carry it; paused it fires nothing.
	f := &schedule.File{}
	MonitorSchedules(w.root, f)
	if _, ok := f.Find(d.Schedule); !ok {
		t.Fatal("running monitor has no schedule")
	}
	if ctx := evalCall(w.r, "POST", LiveMonitorsPath+"/"+d.ID+"/pause", ""); ctx.Response.StatusCode() != 200 {
		t.Fatalf("pause: %s", ctx.Response.Body())
	}
	f = &schedule.File{}
	MonitorSchedules(w.root, f)
	if len(f.Schedules) != 0 {
		t.Fatal("paused monitor still scheduled")
	}
	if ctx := evalCall(w.r, "POST", LiveMonitorsPath+"/"+d.ID+"/run", ""); ctx.Response.StatusCode() != 409 {
		t.Fatalf("run while paused: %d", ctx.Response.StatusCode())
	}
	if ctx := evalCall(w.r, "POST", LiveMonitorsPath+"/"+d.ID+"/resume", ""); ctx.Response.StatusCode() != 200 {
		t.Fatalf("resume: %s", ctx.Response.Body())
	}
	// Update: interval and budget change, agent stays.
	ctx := evalCall(w.r, "PUT", LiveMonitorsPath+"/"+d.ID, `{"interval_min":15,"budget_usd_per_day":9.6,"name":"prod bot"}`)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("update: %s", ctx.Response.Body())
	}
	var u MonitorDetail
	_ = json.Unmarshal(ctx.Response.Body(), &u)
	if u.IntervalMin != 15 || u.CheckBudgetUSD != 0.1 || u.Name != "prod bot" || !strings.HasSuffix(u.Cron, "/15 * * * *") {
		t.Fatalf("updated: %+v", u.MonitorRow)
	}
	if ctx := evalCall(w.r, "PUT", LiveMonitorsPath+"/"+d.ID, `{"agent":"other"}`); ctx.Response.StatusCode() != 422 {
		t.Fatalf("agent change allowed: %d", ctx.Response.StatusCode())
	}
	// List shows it.
	ctx = evalCall(w.r, "GET", LiveMonitorsPath, "")
	var l MonitorList
	if err := json.Unmarshal(ctx.Response.Body(), &l); err != nil || len(l.Monitors) != 1 || l.MaxTasks != livemon.MaxTasks {
		t.Fatalf("list: %s", ctx.Response.Body())
	}
	if strings.Contains(string(ctx.Response.Body()), "badge_secret") {
		t.Fatal("list leaks the badge secret")
	}
	if ctx := evalCall(w.r, "DELETE", LiveMonitorsPath+"/"+d.ID, ""); ctx.Response.StatusCode() != 204 {
		t.Fatalf("delete: %d", ctx.Response.StatusCode())
	}
	if ctx := evalCall(w.r, "GET", LiveMonitorsPath+"/"+d.ID, ""); ctx.Response.StatusCode() != 404 {
		t.Fatalf("after delete: %d", ctx.Response.StatusCode())
	}
}

func TestLiveMonitorBadge(t *testing.T) {
	w := newMonitorWorld(t)
	d := createMonitor(t, w, `{"agent":"support-bot","benchmark":"svc-bench"}`)
	ctx := evalCall(w.r, "POST", LiveMonitorsPath+"/"+d.ID+"/badge", `{"enabled":true}`)
	var on MonitorDetail
	_ = json.Unmarshal(ctx.Response.Body(), &on)
	if on.BadgePath == "" || !strings.HasPrefix(on.BadgePath, APIPrefix+"/live-monitor-badges/local.") {
		t.Fatalf("badge path %q", on.BadgePath)
	}
	ctx = evalCall(w.r, "GET", on.BadgePath, "")
	if ctx.Response.StatusCode() != 200 || !strings.Contains(string(ctx.Response.Body()), "no data") ||
		!strings.HasPrefix(string(ctx.Response.Header.ContentType()), "image/svg+xml") {
		t.Fatalf("badge: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	bad := strings.Replace(on.BadgePath, d.ID+".", d.ID+".x", 1)
	if ctx := evalCall(w.r, "GET", bad, ""); ctx.Response.StatusCode() != 404 {
		t.Fatalf("wrong secret: %d", ctx.Response.StatusCode())
	}
	// Turning it on again makes a new link; the old one stops working.
	ctx = evalCall(w.r, "POST", LiveMonitorsPath+"/"+d.ID+"/badge", `{"enabled":true}`)
	var again MonitorDetail
	_ = json.Unmarshal(ctx.Response.Body(), &again)
	if again.BadgePath == on.BadgePath {
		t.Fatal("badge link not rotated")
	}
	if ctx := evalCall(w.r, "GET", on.BadgePath, ""); ctx.Response.StatusCode() != 404 {
		t.Fatalf("old link: %d", ctx.Response.StatusCode())
	}
	evalCall(w.r, "POST", LiveMonitorsPath+"/"+d.ID+"/badge", `{"enabled":false}`)
	if ctx := evalCall(w.r, "GET", again.BadgePath, ""); ctx.Response.StatusCode() != 404 {
		t.Fatalf("badge off: %d", ctx.Response.StatusCode())
	}
}

func TestMonitorStatsAndStatus(t *testing.T) {
	now := time.Now()
	yes := true
	ms := int64(500)
	s := []livemon.Sample{
		{At: now.Add(-48 * time.Hour), Status: livemon.SampleOK, Passed: 1, Scored: 1},
		{At: now.Add(-time.Hour), Status: livemon.SampleOK, Passed: 2, Scored: 4, CostUSD: 0.1, Runs: []livemon.RunSample{{Pass: &yes, LatencyMS: &ms}}},
		{At: now.Add(-time.Minute), Status: livemon.SampleInfra, Errors: 3},
		{At: now, Status: livemon.SampleSkipped},
	}
	d := monitorStats(s, now.Add(-24*time.Hour))
	if d.Checks != 2 || d.InfraChecks != 1 || *d.PassRate != 0.5 || *d.Uptime != 0.5 || d.Errors != 3 || *d.P50MS != 500 {
		t.Fatalf("day: %+v", d)
	}
	if w := monitorStats(s, now.Add(-7*24*time.Hour)); w.Checks != 3 || w.Scored != 5 {
		t.Fatalf("week: %+v", w)
	}
	m := livemon.Monitor{}
	if got := monitorStatus(m, &livemon.State{Samples: s}, nil); got != "learning" {
		t.Fatalf("status %s", got)
	}
	if got := monitorStatus(m, &livemon.State{}, []livemon.Incident{{Kind: livemon.KindInfra}}); got != "down" {
		t.Fatalf("status %s", got)
	}
}

func TestMonitorEvaluationSpecLoads(t *testing.T) {
	w := newMonitorWorld(t)
	d := createMonitor(t, w, `{"agent":"support-bot","benchmark":"svc-bench","tasks":4,"model":"gpt-x","provider":"openai"}`)
	spec, err := schedule.LoadEvaluationSpec(w.root, livemon.EvaluationRef(d.ID))
	if err != nil {
		t.Fatal(err)
	}
	if spec.TasksPerBenchmark != 4 || spec.Trials != 1 || spec.Arms[0].Model != "gpt-x" || spec.BudgetUSD != d.CheckBudgetUSD {
		t.Fatalf("spec: %+v", spec)
	}
}
