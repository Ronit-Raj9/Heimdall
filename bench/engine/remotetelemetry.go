package engine

// Lane Z14: platform telemetry of the remote coordinator and worker.
//
// Traces: one trace per experiment, its id derived from the org and
// experiment id (telemetry.TraceIDFrom), so the server, every worker and the
// gateway calls they make land on the same trace without shared state. The
// tree is experiment (one span per attach of the scheduler) > lease (one
// slot attempt on a worker) > the worker's stages (prepare, run, upload)
// and the server's request spans for that lease's heartbeats, uploads and
// complete call (the worker sends traceparent on each).
//
// Metrics (selfmetrics.Default, /metrics): leases granted and ended by
// reason, evaluation start latency (attach to first lease) and the time the
// server takes to accept an upload. Labels are fixed small sets; ids go on
// spans only.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/selfmetrics"
	"github.com/maximhq/bifrost/bench/telemetry"
)

var (
	metricLeasesGranted = selfmetrics.Default.CounterVec("heimdall_remote_leases_granted_total",
		"Slot leases handed to workers since start.")
	metricLeasesEnded = selfmetrics.Default.CounterVec("heimdall_remote_leases_ended_total",
		"Slot leases ended since start, by reason: accepted, rejected, lost, released, refused, revoked, stopped, other.", "reason")
	metricEvalStart = selfmetrics.Default.Histogram("heimdall_evaluation_start_seconds",
		"Time from an experiment opening to workers to its first slot leased.",
		[]float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600})
	metricComplete = selfmetrics.Default.Histogram("heimdall_remote_complete_seconds",
		"Time the server takes to verify, store and seal an uploaded run, by result.",
		[]float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}, "result")
)

// coordTel is the coordinator's per-experiment telemetry state.
type coordTel struct {
	mu   sync.Mutex
	exps map[*schedRun]*expTel
}

type expTel struct {
	at     time.Time
	span   *telemetry.Span
	leased bool
}

// leaseTel is one lease's span and how it ended.
type leaseTel struct {
	span   *telemetry.Span
	result string // set by Complete before the lease ends: accepted or rejected
}

// ExperimentTraceID is the trace every span of one experiment joins.
func ExperimentTraceID(orgID, expID string) telemetry.TraceID {
	return telemetry.TraceIDFrom("exp/" + orgID + "/" + expID)
}

func (c *Coordinator) telAttach(r *schedRun) {
	_, span := telemetry.Start(context.Background(), "experiment",
		telemetry.WithParent(telemetry.SpanContext{}),
		telemetry.WithTraceID(ExperimentTraceID(c.opts.OrgID, r.m.ExpID)),
		telemetry.WithAttrs(
			telemetry.String("heimdall.org", c.opts.OrgID),
			telemetry.String("heimdall.experiment", r.m.ExpID),
			telemetry.String("heimdall.suite", r.m.SuiteID),
		))
	c.tel.mu.Lock()
	if c.tel.exps == nil {
		c.tel.exps = map[*schedRun]*expTel{}
	}
	c.tel.exps[r] = &expTel{at: time.Now(), span: span}
	c.tel.mu.Unlock()
}

func (c *Coordinator) telDetach(r *schedRun) {
	c.tel.mu.Lock()
	e := c.tel.exps[r]
	delete(c.tel.exps, r)
	c.tel.mu.Unlock()
	if e != nil {
		e.span.End()
	}
}

// telLeased starts the lease span and counts the lease; the first lease of
// an attach observes the evaluation start latency.
func (c *Coordinator) telLeased(r *schedRun, l *remoteLease) {
	metricLeasesGranted.Inc()
	c.tel.mu.Lock()
	e := c.tel.exps[r]
	first := e != nil && !e.leased
	if first {
		e.leased = true
	}
	c.tel.mu.Unlock()
	if first {
		metricEvalStart.Observe(time.Since(e.at).Seconds())
	}
	parent := telemetry.SpanContext{}
	if e != nil {
		parent = e.span.Context()
	}
	opts := []telemetry.Option{
		telemetry.WithParent(parent),
		telemetry.WithTraceID(ExperimentTraceID(c.opts.OrgID, r.m.ExpID)),
		telemetry.WithAttrs(
			telemetry.String("heimdall.org", c.opts.OrgID),
			telemetry.String("heimdall.experiment", r.m.ExpID),
			telemetry.String("heimdall.slot", l.slot.SlotID),
			telemetry.String("heimdall.task", l.slot.Task),
			telemetry.String("heimdall.agent_version", l.slot.Arm),
			telemetry.String("heimdall.lease", l.id),
			telemetry.String("heimdall.worker", l.worker),
			telemetry.Int("heimdall.attempt", l.attempt),
		),
	}
	_, l.tel.span = telemetry.Start(context.Background(), "lease", opts...)
}

// telComplete records how an upload went, before the lease ends.
func (l *remoteLease) telComplete(began time.Time, reason string) {
	l.tel.result = "accepted"
	if reason != "" {
		l.tel.result = "rejected"
		l.tel.span.SetError("rejected: " + reason)
	}
	metricComplete.Observe(time.Since(began).Seconds(), l.tel.result)
}

// telEnded counts the lease's end and closes its span. why is endLocked's.
func (c *Coordinator) telEnded(l *remoteLease, why string) {
	reason := leaseEndReason(why)
	if l.tel.result != "" {
		reason = l.tel.result
	}
	metricLeasesEnded.Inc(reason)
	l.tel.span.SetAttrs(telemetry.String("heimdall.lease.end", reason))
	switch reason {
	case "lost", "rejected", "other":
		l.tel.span.SetError(why)
	default:
		l.tel.span.SetOK()
	}
	l.tel.span.End()
}

func leaseEndReason(why string) string {
	switch {
	case why == "completed":
		return "accepted"
	case strings.HasPrefix(why, "lost:"):
		return "lost"
	case strings.HasPrefix(why, "released:"):
		return "released"
	case strings.HasPrefix(why, "refused:"):
		return "refused"
	case strings.HasPrefix(why, "revoked:"):
		return "revoked"
	case strings.Contains(why, "scheduler stopped"):
		return "stopped"
	}
	return "other"
}

// --- worker side ---

// workerSlotSpan starts the worker's span for one lease, under the lease
// span the coordinator sent (lease.Traceparent), or on the experiment's
// trace when the server sent none.
func workerSlotSpan(ctx context.Context, lease *schemas.WorkerLease) (context.Context, *telemetry.Span) {
	if !telemetry.Enabled() {
		return ctx, nil
	}
	if sc, ok := telemetry.ParseTraceparent(lease.Traceparent); ok {
		ctx = telemetry.ContextWithRemote(ctx, sc)
	}
	return telemetry.Start(ctx, "worker.slot", telemetry.WithAttrs(
		telemetry.String("heimdall.experiment", lease.ExpID),
		telemetry.String("heimdall.slot", lease.Slot.SlotID),
		telemetry.String("heimdall.task", lease.Slot.Task),
		telemetry.String("heimdall.lease", lease.LeaseID),
		telemetry.String("heimdall.worker", lease.WorkerID),
		telemetry.Int("heimdall.attempt", lease.Attempt),
	))
}
