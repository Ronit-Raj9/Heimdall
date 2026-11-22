package statuspage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/selfmetrics"
)

func TestIncidentLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status", "incidents.json")
	st := NewStore(path)
	if list, err := st.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty store: %v %v", list, err)
	}
	in, err := st.Open(Open{Title: "Evaluations start late", Impact: ImpactMajor, Components: []string{"workers", "workers", "queue"}, Message: "We are looking into it."})
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != StatusInvestigating || len(in.Components) != 2 || len(in.Updates) != 1 {
		t.Fatalf("opened %+v", in)
	}
	// A second store over the same file (the CLI next to the server) sees it.
	other := NewStore(path)
	if _, err := other.Update(in.ID, Change{Status: StatusIdentified, Message: "A worker host ran out of disk."}); err != nil {
		t.Fatal(err)
	}
	list, _ := st.List()
	if len(list) != 1 || list[0].Status != StatusIdentified || len(list[0].Updates) != 2 {
		t.Fatalf("server store did not pick up the CLI edit: %+v", list)
	}
	got, err := st.Update(in.ID, Change{Status: StatusResolved, Message: "Fixed."})
	if err != nil || got.ResolvedAt == nil {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if _, err := st.Update("inc_nope", Change{Message: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	for _, bad := range []Open{{Title: "", Message: "m"}, {Title: "t", Message: ""}, {Title: "t", Message: "m", Impact: "huge"},
		{Title: "t", Message: "m", Status: StatusResolved}, {Title: "t", Message: "m", Components: []string{"Bad Id"}},
		{Title: strings.Repeat("x", maxTitle+1), Message: "m"}} {
		if _, err := st.Open(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := st.Remove(in.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := other.List(); len(list) != 0 {
		t.Fatalf("removed incident still listed: %+v", list)
	}
}

func TestPageLevels(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "incidents.json"))
	var runs atomic.Int32
	dbDown := false
	checks := []Check{
		APICheck(),
		ErrorCheck(ComponentDatabase, "Database", "not reachable", func(context.Context) error {
			runs.Add(1)
			if dbDown {
				return errors.New("dial tcp 10.0.0.5:5432: secret detail")
			}
			return nil
		}),
		{ID: "panics", Name: "Panics", Run: func(context.Context) (Level, string) { panic("boom") }},
	}
	svc := New(checks, st, "1.2.3")
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	st.now = svc.now

	p := svc.Page(context.Background())
	if p.Level != Unknown || p.Components[0].Level != Operational || p.Components[1].Level != Operational || p.Components[2].Level != Unknown {
		t.Fatalf("page %+v", p)
	}
	// Cached: a second read inside CacheFor runs no check.
	dbDown = true
	svc.Page(context.Background())
	if runs.Load() != 1 {
		t.Fatalf("checks ran %d times inside the cache window", runs.Load())
	}
	now = now.Add(CacheFor)
	p = svc.Page(context.Background())
	if p.Components[1].Level != MajorOutage || p.Components[1].Detail != "not reachable" || p.Level != MajorOutage {
		t.Fatalf("db down: %+v", p.Components[1])
	}
	if strings.Contains(p.Components[1].Detail, "10.0.0.5") {
		t.Fatal("error text leaked to the public page")
	}

	dbDown = false
	now = now.Add(CacheFor)
	in, _ := st.Open(Open{Title: "API slow", Impact: ImpactMinor, Components: []string{"api"}, Message: "Looking."})
	p = svc.Page(context.Background())
	if p.Components[0].Level != Degraded || p.Components[0].Incident != in.ID || len(p.Incidents) != 1 {
		t.Fatalf("incident did not raise the API: %+v", p)
	}
	future := now.Add(48 * time.Hour)
	if _, err := st.Open(Open{Title: "Database upgrade", Impact: ImpactMaintenance, Status: StatusScheduled, ScheduledFor: &future, Components: []string{"database"}, Message: "Planned."}); err != nil {
		t.Fatal(err)
	}
	p = svc.Page(context.Background())
	if p.Components[1].Level != Operational || len(p.Incidents) != 2 {
		t.Fatalf("future maintenance must be listed but not applied: %+v", p)
	}
	if _, err := st.Update(in.ID, Change{Status: StatusResolved, Message: "Done."}); err != nil {
		t.Fatal(err)
	}
	p = svc.Page(context.Background())
	if p.Components[0].Level != Operational || len(p.Recent) != 1 || len(p.Incidents) != 1 {
		t.Fatalf("resolved incident: %+v", p)
	}
	now = now.Add(RecentFor + time.Hour)
	if p = svc.Page(context.Background()); len(p.Recent) != 0 {
		t.Fatal("old resolved incidents must drop off")
	}
}

func TestMetricChecks(t *testing.T) {
	reg := selfmetrics.New()
	workers := map[string]float64{}
	age, low, queue := 0.0, 0.0, 0.0
	reg.Gauge("heimdall_workers", "", func(context.Context) []selfmetrics.Sample {
		var out []selfmetrics.Sample
		for s, n := range workers {
			out = append(out, selfmetrics.Sample{Labels: map[string]string{"state": s}, Value: n})
		}
		return out
	})
	reg.Gauge("heimdall_lease_oldest_heartbeat_age_seconds", "", func(context.Context) []selfmetrics.Sample { return []selfmetrics.Sample{{Value: age}} })
	reg.Gauge("heimdall_workers_disk_low", "", func(context.Context) []selfmetrics.Sample { return []selfmetrics.Sample{{Value: low}} })
	reg.Gauge("heimdall_job_queue_depth", "", func(context.Context) []selfmetrics.Sample {
		return []selfmetrics.Sample{{Labels: map[string]string{"kind": "experiment.run"}, Value: queue}, {Labels: map[string]string{"kind": "import"}, Value: 999}}
	})
	w, q := WorkersCheck(reg), QueueCheck(reg)
	ctx := context.Background()
	cases := []struct {
		name    string
		set     func()
		want    Level
		wantQ   Level
		wantDet string
	}{
		{"none", func() {}, Unknown, Operational, "no worker connected"},
		{"healthy", func() { workers = map[string]float64{"idle": 2, "running": 1} }, Operational, Operational, ""},
		{"late", func() { age = 120 }, Degraded, Operational, "a running evaluation reported late"},
		{"disk", func() { age = 0; low = 1 }, Degraded, Operational, "some workers are low on disk"},
		{"all lost", func() { low = 0; workers = map[string]float64{"lost": 3} }, MajorOutage, Operational, "no worker is answering"},
		{"queue busy", func() { workers = map[string]float64{"idle": 1}; queue = QueueBusy + 1 }, Operational, Degraded, ""},
	}
	for _, c := range cases {
		c.set()
		lvl, det := w.Run(ctx)
		ql, _ := q.Run(ctx)
		if lvl != c.want || det != c.wantDet || ql != c.wantQ {
			t.Errorf("%s: workers %s %q queue %s", c.name, lvl, det, ql)
		}
	}
}

func TestHTTPProbes(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	checks := ParseProbes("gateway="+ok.URL+", fault-proxy="+bad.URL+",broken,x=ftp://h,Bad="+ok.URL, nil)
	if len(checks) != 2 || checks[0].Name != "Model gateway" || checks[1].Name != "Fault proxy" {
		t.Fatalf("probes %+v", checks)
	}
	if l, _ := checks[0].Run(context.Background()); l != Operational {
		t.Fatalf("ok probe: %s", l)
	}
	if l, d := checks[1].Run(context.Background()); l != MajorOutage || d != "not answering" {
		t.Fatalf("bad probe: %s %s", l, d)
	}
}
