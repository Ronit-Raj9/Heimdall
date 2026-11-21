package main

// Self-observability (PROD2 N9): the server's Prometheus metrics, served on
// server.metrics_listen (a separate admin address, never the public site):
// API latency, job queue depth and jobs by status (experiment runs are
// experiment.run jobs), leases in flight, workers and uploaded runs.

import (
	"context"
	"net"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/selfmetrics"
	"github.com/valyala/fasthttp"
)

// registerJobMetrics reads job counts from the jobs table on every scrape.
func registerJobMetrics(reg *selfmetrics.Registry, r *jobs.Runner) {
	counts := func(ctx context.Context) []jobs.KindStatusCount {
		cs, err := r.CountByStatus(ctx)
		if err != nil {
			return nil
		}
		return cs
	}
	reg.Gauge("heimdall_job_queue_depth", "Jobs waiting for a worker, by kind.", func(ctx context.Context) []selfmetrics.Sample {
		var out []selfmetrics.Sample
		for _, c := range counts(ctx) {
			if c.Status == jobs.StatusQueued {
				out = append(out, selfmetrics.Sample{Labels: map[string]string{"kind": c.Kind}, Value: float64(c.Count)})
			}
		}
		return out
	})
	reg.Gauge("heimdall_jobs", "Jobs by kind and status; experiment runs are kind experiment.run.", func(ctx context.Context) []selfmetrics.Sample {
		var out []selfmetrics.Sample
		for _, c := range counts(ctx) {
			out = append(out, selfmetrics.Sample{Labels: map[string]string{"kind": c.Kind, "status": string(c.Status)}, Value: float64(c.Count)})
		}
		return out
	})
}

// registerCoordinatorMetrics reads the live state of every coordinator
// coords returns (one in local worker mode, one per organization hosted).
func registerCoordinatorMetrics(reg *selfmetrics.Registry, coords func() []*engine.Coordinator) {
	total := func() engine.CoordinatorStats {
		sum := engine.CoordinatorStats{Workers: map[string]int{}, Runs: map[string]int{}}
		for _, c := range coords() {
			st := c.Stats()
			sum.Leases += st.Leases
			sum.Attached += st.Attached
			sum.WorkersDiskLow += st.WorkersDiskLow
			if st.OldestBeat > sum.OldestBeat {
				sum.OldestBeat = st.OldestBeat
			}
			for k, v := range st.Workers {
				sum.Workers[k] += v
			}
			for k, v := range st.Runs {
				sum.Runs[k] += v
			}
		}
		return sum
	}
	one := func(v float64) []selfmetrics.Sample { return []selfmetrics.Sample{{Value: v}} }
	reg.Gauge("heimdall_leases_in_flight", "Slot and job leases out on workers.", func(context.Context) []selfmetrics.Sample {
		return one(float64(total().Leases))
	})
	reg.Gauge("heimdall_lease_oldest_heartbeat_age_seconds", "Longest time since a lease's last heartbeat.", func(context.Context) []selfmetrics.Sample {
		return one(total().OldestBeat.Seconds())
	})
	reg.Gauge("heimdall_remote_experiments", "Experiments open to workers.", func(context.Context) []selfmetrics.Sample {
		return one(float64(total().Attached))
	})
	reg.Gauge("heimdall_workers", "Registered workers by state.", func(context.Context) []selfmetrics.Sample {
		var out []selfmetrics.Sample
		for state, n := range total().Workers {
			out = append(out, selfmetrics.Sample{Labels: map[string]string{"state": state}, Value: float64(n)})
		}
		return out
	})
	reg.Gauge("heimdall_workers_disk_low", "Workers taking no work because their disk is low.", func(context.Context) []selfmetrics.Sample {
		return one(float64(total().WorkersDiskLow))
	})
	reg.Counter("heimdall_remote_runs_total", "Runs uploaded by workers since start, by result and outcome.", func(context.Context) []selfmetrics.Sample {
		var out []selfmetrics.Sample
		for key, n := range total().Runs {
			l := map[string]string{"result": "rejected"}
			if key != "rejected" {
				l["result"], l["outcome"] = "accepted", key[len("accepted/"):]
			}
			out = append(out, selfmetrics.Sample{Labels: l, Value: float64(n)})
		}
		return out
	})
}

// serveMetrics serves GET /metrics on addr until ctx ends; empty addr turns
// it off. The address is meant for the operator's network only (compose:
// the internal network, not published by Caddy).
func serveMetrics(ctx context.Context, addr string, logger schemas.Logger) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("metrics endpoint not started", "listen", addr, "error", err.Error())
		return
	}
	srv := &fasthttp.Server{Handler: func(rc *fasthttp.RequestCtx) {
		if string(rc.Path()) != "/metrics" {
			rc.SetStatusCode(fasthttp.StatusNotFound)
			return
		}
		selfmetrics.Default.Handler(rc)
	}, Name: "heimdall-metrics", ReadTimeout: 10 * time.Second, NoDefaultServerHeader: true}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown()
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && ctx.Err() == nil {
			logger.Error("metrics endpoint stopped", "error", err.Error())
		}
	}()
	logger.Info("metrics on http://" + ln.Addr().String() + "/metrics")
}
