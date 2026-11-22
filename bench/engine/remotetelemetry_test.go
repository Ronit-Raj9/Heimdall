package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/telemetry"
)

// TestRemoteTelemetryTrace: with tracing on, one experiment's spans from
// the coordinator and the worker share the experiment's derived trace and
// nest experiment > lease > worker.slot > run/upload; the lease metrics
// count every accepted lease.
func TestRemoteTelemetryTrace(t *testing.T) {
	var mu sync.Mutex
	var spans []map[string]any
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []map[string]any `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("collector: %v", err)
		}
		mu.Lock()
		for _, rs := range body.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				spans = append(spans, ss.Spans...)
			}
		}
		mu.Unlock()
	}))
	defer col.Close()
	tr := telemetry.NewTracer(telemetry.Config{TracesURL: col.URL + "/v1/traces", SampleRatio: 1})
	telemetry.SetDefault(tr)
	defer telemetry.SetDefault(nil)
	acceptedBefore := metricLeasesEnded.Value("accepted")
	grantedBefore := metricLeasesGranted.Value()

	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	ctx := context.Background()
	for range rw.m.Slots {
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease while slots are pending")
		}
		if _, ok := telemetry.ParseTraceparent(l.Traceparent); !ok {
			t.Fatalf("lease carries no traceparent: %q", l.Traceparent)
		}
		if res := w.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run not accepted: %+v", res)
		}
	}
	done()
	tr.Flush(ctx)

	n := float64(len(rw.m.Slots))
	if got := metricLeasesEnded.Value("accepted") - acceptedBefore; got != n {
		t.Fatalf("accepted lease ends = %v, want %v", got, n)
	}
	if got := metricLeasesGranted.Value() - grantedBefore; got != n {
		t.Fatalf("granted = %v, want %v", got, n)
	}

	mu.Lock()
	defer mu.Unlock()
	want := ExperimentTraceID("", rw.m.ExpID).String()
	byName := map[string][]map[string]any{}
	byID := map[string]map[string]any{}
	for _, s := range spans {
		if s["traceId"] != want {
			t.Fatalf("span %v off the experiment trace", s["name"])
		}
		byName[s["name"].(string)] = append(byName[s["name"].(string)], s)
		byID[s["spanId"].(string)] = s
	}
	if len(byName["experiment"]) != 1 || len(byName["lease"]) != len(rw.m.Slots) || len(byName["worker.slot"]) != len(rw.m.Slots) ||
		len(byName["run"]) != len(rw.m.Slots) || len(byName["upload"]) != len(rw.m.Slots) {
		t.Fatalf("span counts: experiment %d lease %d worker.slot %d run %d upload %d", len(byName["experiment"]), len(byName["lease"]),
			len(byName["worker.slot"]), len(byName["run"]), len(byName["upload"]))
	}
	parentName := func(s map[string]any) string {
		p, ok := byID[s["parentSpanId"].(string)]
		if !ok {
			return ""
		}
		return p["name"].(string)
	}
	for child, parent := range map[string]string{"lease": "experiment", "worker.slot": "lease", "run": "worker.slot", "upload": "worker.slot"} {
		for _, s := range byName[child] {
			if got := parentName(s); got != parent {
				t.Fatalf("%s parent = %q, want %q", child, got, parent)
			}
		}
	}
}
