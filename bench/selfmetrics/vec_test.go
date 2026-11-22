package selfmetrics

import (
	"context"
	"strings"
	"testing"
)

func TestVecsRender(t *testing.T) {
	r := New()
	c := r.CounterVec("heimdall_test_total", "Things.", "reason")
	c.Inc("lost")
	c.Add(2, "accepted")
	c.Add(-1, "accepted") // ignored: counters only go up
	h := r.Histogram("heimdall_test_seconds", "Waits.", []float64{10, 1}, "result")
	h.Observe(0.5, "ok")
	h.Observe(5, "ok")
	h.Observe(100, "ok")
	out := r.Render(context.Background())
	for _, want := range []string{
		"# TYPE heimdall_test_total counter",
		`heimdall_test_total{reason="accepted"} 2`,
		`heimdall_test_total{reason="lost"} 1`,
		"# TYPE heimdall_test_seconds histogram",
		`heimdall_test_seconds_bucket{le="1",result="ok"} 1`,
		`heimdall_test_seconds_bucket{le="10",result="ok"} 2`,
		`heimdall_test_seconds_bucket{le="+Inf",result="ok"} 3`,
		`heimdall_test_seconds_sum{result="ok"} 105.5`,
		`heimdall_test_seconds_count{result="ok"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	snap := r.Snapshot(context.Background())
	var found bool
	for _, f := range snap.Histograms {
		if f.Name == "heimdall_test_seconds" {
			found = true
			if p := f.Points[0]; p.Count != 3 || len(p.Counts) != 3 || p.Counts[2] != 1 {
				t.Fatalf("snapshot point %+v", p)
			}
		}
	}
	if !found || snap.Histograms[0].Name != "heimdall_http_request_duration_seconds" {
		t.Fatalf("snapshot histograms %+v", snap.Histograms)
	}
}
