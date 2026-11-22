package statuspage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/selfmetrics"
)

// sharedWorkersReg is a registry with the shared pool gauge and the worker
// gauge of every pool.
func sharedWorkersReg(shared, all *map[string]float64) *selfmetrics.Registry {
	reg := selfmetrics.New()
	gauge := func(m *map[string]float64) func(context.Context) []selfmetrics.Sample {
		return func(context.Context) []selfmetrics.Sample {
			var out []selfmetrics.Sample
			for s, n := range *m {
				out = append(out, selfmetrics.Sample{Labels: map[string]string{"state": s}, Value: n})
			}
			return out
		}
	}
	reg.Gauge("heimdall_shared_workers", "", gauge(shared))
	reg.Gauge("heimdall_workers", "", gauge(all))
	return reg
}

func TestWorkersCheckSharedOnly(t *testing.T) {
	shared := map[string]float64{}
	all := map[string]float64{}
	w := WorkersCheck(sharedWorkersReg(&shared, &all))
	ctx := context.Background()
	cases := []struct {
		name        string
		shared, all map[string]float64
		want        Level
		wantDetail  string
	}{
		{"no shared pool", nil, map[string]float64{"idle": 1}, Unknown, "no shared pool on this server"},
		{"lost org worker does not count", map[string]float64{"running": 1}, map[string]float64{"running": 1, "lost": 4}, Operational, ""},
		{"some shared lost", map[string]float64{"idle": 1, "lost": 1}, map[string]float64{"idle": 1, "lost": 1}, Degraded, "some shared workers stopped answering"},
		{"all shared lost", map[string]float64{"lost": 2}, map[string]float64{"idle": 3, "lost": 2}, MajorOutage, "no shared worker is answering"},
	}
	for _, c := range cases {
		shared, all = c.shared, c.all
		if shared == nil {
			shared = map[string]float64{}
		}
		lvl, det := w.Run(ctx)
		if lvl != c.want || det != c.wantDetail {
			t.Errorf("%s: %s %q, want %s %q", c.name, lvl, det, c.want, c.wantDetail)
		}
	}
}

func TestRunsCheckWindow(t *testing.T) {
	var total, infra float64
	reg := selfmetrics.New()
	reg.Counter("heimdall_remote_runs_total", "", func(context.Context) []selfmetrics.Sample {
		return []selfmetrics.Sample{
			{Labels: map[string]string{"result": "accepted", "outcome": "aborted_infra"}, Value: infra},
			{Labels: map[string]string{"result": "accepted", "outcome": "passed"}, Value: total - infra},
			{Labels: map[string]string{"result": "rejected"}, Value: 50},
		}
	})
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := RunsCheck(reg, func() time.Time { return now })
	ctx := context.Background()
	step := func(d time.Duration, tot, inf float64, want Level, wantDetail string) {
		t.Helper()
		now = now.Add(d)
		total, infra = tot, inf
		if lvl, det := c.Run(ctx); lvl != want || det != wantDetail {
			t.Fatalf("at %s with %v/%v: %s %q, want %s %q", now.Format(time.Kitchen), inf, tot, lvl, det, want, wantDetail)
		}
	}
	step(0, 100, 0, Operational, "")                                                // first sample: nothing in the window yet
	step(time.Minute, 110, 8, MajorOutage, "runs are failing for platform reasons") // 8 of 10
	step(time.Minute, 114, 8, Degraded, "some runs fail for platform reasons")      // 8 of 14
	step(time.Minute, 130, 8, Operational, "")                                      // 8 of 30
}

func TestRunsCheckOldSamplesAndReset(t *testing.T) {
	var total, infra float64
	reg := selfmetrics.New()
	reg.Counter("heimdall_remote_runs_total", "", func(context.Context) []selfmetrics.Sample {
		return []selfmetrics.Sample{
			{Labels: map[string]string{"result": "accepted", "outcome": "aborted_infra"}, Value: infra},
			{Labels: map[string]string{"result": "accepted", "outcome": "passed"}, Value: total - infra},
		}
	})
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	c := RunsCheck(reg, func() time.Time { return now })
	ctx := context.Background()
	run := func() Level { l, _ := c.Run(ctx); return l }
	total, infra = 0, 0
	run()
	now, total, infra = now.Add(time.Minute), 10, 8
	if l := run(); l != MajorOutage {
		t.Fatalf("8 of 10: %s", l)
	}
	now = now.Add(20 * time.Minute) // nothing new: the bad runs fall out of the window
	if l := run(); l != Operational {
		t.Fatalf("after the window: %s", l)
	}
	now, total, infra = now.Add(time.Minute), 3, 3 // the counter went down: restart
	if l := run(); l != Operational {
		t.Fatalf("after a reset: %s", l)
	}
	now, total, infra = now.Add(time.Minute), 9, 9
	if l := run(); l != MajorOutage {
		t.Fatalf("6 of 6 after the reset: %s", l)
	}
}

func TestAutoIncidentOpensAndResolves(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "incidents.json"))
	level, detail := Operational, ""
	svc := New([]Check{{ID: ComponentGateway, Name: "Model gateway", Run: func(context.Context) (Level, string) { return level, detail }}}, st, "v").WithAutoIncidents()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	st.now = svc.now
	op, err := st.Open(Open{Title: "Gateway maintenance notes", Impact: ImpactMinor, Components: []string{ComponentGateway}, Message: "Operator note."})
	if err != nil {
		t.Fatal(err)
	}
	page := func(d time.Duration) Page {
		now = now.Add(d)
		return svc.Page(context.Background())
	}
	autoOpen := func(p Page) []Incident {
		var out []Incident
		for _, in := range p.Incidents {
			if in.Auto {
				out = append(out, in)
			}
		}
		return out
	}
	level, detail = MajorOutage, "not reachable"
	page(0)
	if got := autoOpen(page(4 * time.Minute)); len(got) != 0 {
		t.Fatalf("opened before 5 minutes: %+v", got)
	}
	p := page(time.Minute + time.Second)
	got := autoOpen(p)
	if len(got) != 1 || got[0].Title != "Model gateway: not reachable" || got[0].Impact != ImpactCritical ||
		got[0].Updates[0].Message != autoOpenMessage || len(got[0].Components) != 1 || got[0].Components[0] != ComponentGateway {
		t.Fatalf("automatic incident %+v", got)
	}
	if got := autoOpen(page(10 * time.Minute)); len(got) != 1 {
		t.Fatalf("a second incident opened: %+v", got)
	}
	level, detail = Operational, ""
	page(time.Minute)
	if got := autoOpen(page(3 * time.Minute)); len(got) != 1 {
		t.Fatalf("resolved before 5 minutes good: %+v", got)
	}
	p = page(2*time.Minute + time.Second)
	if got := autoOpen(p); len(got) != 0 {
		t.Fatalf("not resolved after 5 minutes good: %+v", got)
	}
	var resolved, operator *Incident
	for i := range p.Recent {
		if p.Recent[i].Auto {
			resolved = &p.Recent[i]
		}
	}
	for i := range p.Incidents {
		if p.Incidents[i].ID == op.ID {
			operator = &p.Incidents[i]
		}
	}
	if resolved == nil || resolved.Updates[len(resolved.Updates)-1].Message != autoResolveMessage {
		t.Fatalf("resolved automatic incident not under recent: %+v", p.Recent)
	}
	if operator == nil || operator.Status != StatusInvestigating || len(operator.Updates) != 1 {
		t.Fatalf("operator incident touched: %+v", operator)
	}
}
