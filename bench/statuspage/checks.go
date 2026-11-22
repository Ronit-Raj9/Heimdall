package statuspage

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/selfmetrics"
)

// Component ids the built-in checks use. Incidents name components by id.
const (
	ComponentAPI      = "api"
	ComponentDatabase = "database"
	ComponentStorage  = "storage"
	ComponentWorkers  = "workers"
	ComponentQueue    = "queue"
	ComponentGateway  = "gateway"
)

// Thresholds of the built-in checks.
const (
	// HeartbeatLate is the oldest heartbeat age that degrades the worker
	// pool; the coordinator drops a lease at 2 minutes.
	HeartbeatLate = 90 * time.Second
	// QueueBusy is the number of waiting evaluations that degrades the queue.
	QueueBusy = 50
	// SlowProbe degrades an HTTP probe that answers but takes this long.
	SlowProbe = 2 * time.Second
)

// APICheck is the web and API component: answering at all is operational.
func APICheck() Check {
	return Check{ID: ComponentAPI, Name: "Website and API", Run: func(context.Context) (Level, string) {
		return Operational, ""
	}}
}

// ErrorCheck wraps a check that returns an error (a readiness check): an
// error is down with the fixed public sentence down, never the error text.
func ErrorCheck(id, name, down string, fn func(ctx context.Context) error) Check {
	return Check{ID: id, Name: name, Run: func(ctx context.Context) (Level, string) {
		if err := fn(ctx); err != nil {
			return MajorOutage, down
		}
		return Operational, ""
	}}
}

// HTTPCheck probes url with GET: 2xx is operational (degraded when slower
// than SlowProbe), anything else a major outage.
func HTTPCheck(id, name, url string, client *http.Client) Check {
	if client == nil {
		client = &http.Client{Timeout: CheckTimeout}
	}
	return Check{ID: id, Name: name, Run: func(ctx context.Context) (Level, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return Unknown, "not configured"
		}
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return MajorOutage, "not reachable"
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return MajorOutage, "not answering"
		}
		if time.Since(start) > SlowProbe {
			return Degraded, "answering slowly"
		}
		return Operational, ""
	}}
}

// metricsReader gathers a registry once per check run.
type metricsReader interface {
	Gather(ctx context.Context) []selfmetrics.Family
}

func family(fs []selfmetrics.Family, name string) *selfmetrics.Family {
	for i := range fs {
		if fs[i].Name == name {
			return &fs[i]
		}
	}
	return nil
}

func sum(f *selfmetrics.Family, match func(map[string]string) bool) (float64, bool) {
	if f == nil {
		return 0, false
	}
	var v float64
	for _, s := range f.Samples {
		if match == nil || match(s.Labels) {
			v += s.Value
		}
	}
	return v, true
}

// WorkersCheck reads the platform's shared pool from the server's /metrics
// registry (heimdall_shared_workers, the coordinators' live state; fix wave
// 2, N148): an organization's own workers are not a platform component and
// never change this line. It also reads the oldest heartbeat and low disks.
// With no shared worker registered the level is unknown: the platform may
// simply have no shared pool.
func WorkersCheck(reg metricsReader) Check {
	return Check{ID: ComponentWorkers, Name: "Evaluation workers", Run: func(ctx context.Context) (Level, string) {
		fs := reg.Gather(ctx)
		workers := family(fs, "heimdall_shared_workers")
		total, ok := sum(workers, nil)
		if !ok || total == 0 {
			return Unknown, "no shared pool on this server"
		}
		lost, _ := sum(workers, func(l map[string]string) bool { return l["state"] == "lost" })
		if lost == total {
			return MajorOutage, "no shared worker is answering"
		}
		lvl, detail := Operational, ""
		if lost > 0 {
			lvl, detail = Degraded, "some shared workers stopped answering"
		}
		if age, _ := sum(family(fs, "heimdall_lease_oldest_heartbeat_age_seconds"), nil); age > HeartbeatLate.Seconds() {
			lvl, detail = Worse(lvl, Degraded), "a running evaluation reported late"
		}
		if low, _ := sum(family(fs, "heimdall_workers_disk_low"), nil); low > 0 && lvl == Operational {
			lvl, detail = Degraded, "some workers are low on disk"
		}
		return lvl, detail
	}}
}

// QueueCheck reads the job queue depth: more than QueueBusy waiting
// evaluations degrades it (evaluations start late, none is lost).
func QueueCheck(reg metricsReader) Check {
	return Check{ID: ComponentQueue, Name: "Evaluation queue", Run: func(ctx context.Context) (Level, string) {
		f := family(reg.Gather(ctx), "heimdall_job_queue_depth")
		n, ok := sum(f, func(l map[string]string) bool { return strings.HasPrefix(l["kind"], "experiment") })
		if !ok {
			return Unknown, ""
		}
		if n > QueueBusy {
			return Degraded, "evaluations are waiting longer than usual to start"
		}
		return Operational, ""
	}}
}

// ParseProbes reads HEIMDALL_STATUS_PROBES: comma-separated id=url pairs
// (for example gateway=http://bifrost:8080/health). Known ids get their
// public name; others are named after the id.
func ParseProbes(s string, client *http.Client) []Check {
	names := map[string]string{ComponentGateway: "Model gateway"}
	var out []Check
	for _, part := range strings.Split(s, ",") {
		id, url, ok := strings.Cut(strings.TrimSpace(part), "=")
		id, url = strings.TrimSpace(id), strings.TrimSpace(url)
		if !ok || id == "" || !(strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) {
			continue
		}
		if _, err := checkComponents([]string{id}); err != nil {
			continue
		}
		name := names[id]
		if name == "" {
			name = strings.ToUpper(id[:1]) + strings.ReplaceAll(id[1:], "-", " ")
		}
		out = append(out, HTTPCheck(id, name, url, client))
	}
	return out
}
