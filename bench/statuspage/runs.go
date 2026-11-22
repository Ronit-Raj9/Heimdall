package statuspage

// The "Evaluation runs" line (fix wave 2, N148): a model gateway that
// answers /health but times out every model call shows up only in run
// outcomes. RunsCheck reads the heimdall_remote_runs_total counter (runs
// uploaded by workers, by outcome) and judges the runs that finished in the
// last RunsWindow by their share of aborted_infra (failed for platform
// reasons).

import (
	"context"
	"sync"
	"time"
)

// Thresholds of the runs check.
const (
	// RunsWindow is the sliding window the runs check looks at.
	RunsWindow = 15 * time.Minute
	// RunsMinimum is the fewest runs in the window the check judges.
	RunsMinimum = 5
	// RunsInfraDegraded and RunsInfraMajor are the shares of runs failed
	// for platform reasons that degrade the line or make it a major outage.
	RunsInfraDegraded = 0.30
	RunsInfraMajor    = 0.80
)

// ComponentRuns is the evaluation runs line.
const ComponentRuns = "runs"

// runsSample is the counter at one check run.
type runsSample struct {
	at           time.Time
	total, infra float64
}

// RunsCheck judges the runs uploaded in the last RunsWindow. It keeps one
// sample per check run in memory: a server restart (or any counter that went
// down) starts the window again.
func RunsCheck(reg metricsReader, now func() time.Time) Check {
	if now == nil {
		now = time.Now
	}
	var mu sync.Mutex
	var ring []runsSample
	return Check{ID: ComponentRuns, Name: "Evaluation runs", Run: func(ctx context.Context) (Level, string) {
		f := family(reg.Gather(ctx), "heimdall_remote_runs_total")
		total, _ := sum(f, func(l map[string]string) bool { return l["result"] == "accepted" })
		infra, _ := sum(f, func(l map[string]string) bool { return l["result"] == "accepted" && l["outcome"] == "aborted_infra" })
		at := now()
		mu.Lock()
		defer mu.Unlock()
		if n := len(ring); n > 0 && (total < ring[n-1].total || infra < ring[n-1].infra) {
			ring = nil // the counter went down: the server restarted
		}
		ring = append(ring, runsSample{at: at, total: total, infra: infra})
		// Keep the newest sample at or before the window start as the base,
		// drop everything older.
		start := at.Add(-RunsWindow)
		drop := 0
		for drop+1 < len(ring) && !ring[drop+1].at.After(start) {
			drop++
		}
		ring = append(ring[:0:0], ring[drop:]...)
		base := ring[0]
		runs, failed := total-base.total, infra-base.infra
		if runs < RunsMinimum {
			return Operational, ""
		}
		switch share := failed / runs; {
		case share >= RunsInfraMajor:
			return MajorOutage, "runs are failing for platform reasons"
		case share >= RunsInfraDegraded:
			return Degraded, "some runs fail for platform reasons"
		}
		return Operational, ""
	}}
}
