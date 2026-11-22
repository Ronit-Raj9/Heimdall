package main

// Lane Z14: platform telemetry. The server and every runner (worker,
// shared runner) send OpenTelemetry spans, and optionally metrics, to the
// OTLP endpoint the OTEL_* environment names (bench/telemetry,
// docs/monitoring.md). Nothing is sent when no endpoint is set.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/maximhq/bifrost/bench/selfmetrics"
	"github.com/maximhq/bifrost/bench/telemetry"
)

// startTelemetry configures the process tracer for service and flushes it
// on exit. It also exposes the exporter's own counters on /metrics.
func startTelemetry(service string) {
	shutdown := telemetry.Setup(service, benchVersion, func(msg string) { fmt.Fprintln(os.Stderr, "warn: "+msg) }, selfmetrics.Default)
	if telemetry.Enabled() {
		registerTelemetryMetrics(selfmetrics.Default)
	}
	onExit(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdown(ctx)
	})
}

func registerTelemetryMetrics(reg *selfmetrics.Registry) {
	reg.Counter("heimdall_telemetry_spans_total", "Spans this process handed to the OTLP exporter, by result (sent, dropped when the queue was full, failed to send).",
		func(context.Context) []selfmetrics.Sample {
			st := telemetry.Default().Stats()
			return []selfmetrics.Sample{
				{Labels: map[string]string{"result": "sent"}, Value: float64(st.Sent)},
				{Labels: map[string]string{"result": "dropped"}, Value: float64(st.Dropped)},
				{Labels: map[string]string{"result": "failed"}, Value: float64(st.Failed)},
			}
		})
}
