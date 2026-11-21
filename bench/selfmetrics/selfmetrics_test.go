package selfmetrics

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestArea(t *testing.T) {
	for path, want := range map[string]string{
		"/api/bench/experiments/exp_123/runs": "experiments",
		"/api/bench/orgs/acme/runs/r_1":       "runs",
		"/api/bench/orgs/acme":                "orgs",
		"/healthz":                            "health",
		"/":                                   "ui",
		"/assets/app.js":                      "ui",
		"/api/bench/Exp_ID":                   "other",
	} {
		if got := Area(path); got != want {
			t.Errorf("Area(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	r := New()
	r.Observe("GET", "/api/bench/runs/r_1", 200, 30*time.Millisecond)
	r.Observe("GET", "/api/bench/runs/r_2", 200, 2*time.Second)
	r.Observe("BREW", "/x", 500, time.Millisecond)
	r.Gauge("heimdall_jobs", "Jobs \"by\" status.", func(context.Context) []Sample {
		return []Sample{{Labels: map[string]string{"status": "queued", "kind": `a"b`}, Value: 3}}
	})
	r.Gauge("heimdall_jobs", "Jobs by status.", func(context.Context) []Sample { // replaces the first
		return []Sample{{Labels: map[string]string{"status": "queued"}, Value: 4}}
	})
	out := r.Render(context.Background())
	for _, want := range []string{
		"# TYPE heimdall_http_request_duration_seconds histogram",
		`heimdall_http_request_duration_seconds_bucket{area="runs",code="2xx",le="0.05",method="GET"} 1`,
		`heimdall_http_request_duration_seconds_bucket{area="runs",code="2xx",le="+Inf",method="GET"} 2`,
		`heimdall_http_request_duration_seconds_count{area="runs",code="2xx",method="GET"} 2`,
		`method="OTHER"`,
		"# TYPE heimdall_jobs gauge",
		`heimdall_jobs{status="queued"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "# TYPE heimdall_jobs") != 1 {
		t.Errorf("gauge registered twice:\n%s", out)
	}
}
