package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("eventcollector", "run the per-run agent event collector alone (POST /events, POST /otlp/v1/traces), for a run with no proxied services", runEventCollector)
}

// runEventCollector serves the agent event collector without routes. The
// environment kernel runs it as the run's tool proxy container when the run
// has no proxied services, so the agent reaches it at the same address.
func runEventCollector(args []string) int {
	fs := flag.NewFlagSet("eventcollector", flag.ContinueOnError)
	listen := fs.String("listen", ":9200", "address the agent reaches the collector on")
	ledger := fs.String("ledger", "/ledger/agent-events.jsonl", "ledger file, one reported event per line")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	logger := utils.NewStdLogger(os.Stderr, false)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("event collector listening on %s (ledger %s)", *listen, *ledger)
	if err := toolproxy.ServeCollector(ctx, toolproxy.CollectorConfig{Listen: *listen, LedgerPath: *ledger}); err != nil && ctx.Err() == nil {
		return fail("event collector: %v", err)
	}
	return exitOK
}
