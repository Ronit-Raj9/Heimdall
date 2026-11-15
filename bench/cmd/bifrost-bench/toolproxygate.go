package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/utils"
)

// runToolGate runs the remote tool gate (Remote coverage): the environment
// kernel starts it in the container bench-rt-<run id> beside the tool proxy
// of a remote agent's run with remote.tools expose:
//
//	bifrost-bench toolproxy gate -listen :9201 -upstream http://toolproxy:9200 -run <run id> -until <RFC 3339>
//
// The sha256 of the run's bearer token comes from BENCH_GATE_TOKEN_SHA256.
func runToolGate(args []string) int {
	fs := flag.NewFlagSet("toolproxy gate", flag.ContinueOnError)
	listen := fs.String("listen", ":9201", "address the gate listens on")
	upstream := fs.String("upstream", "http://toolproxy:9200", "the run's tool proxy")
	run := fs.String("run", "", "the only run id the gate serves")
	until := fs.String("until", "", "RFC 3339 time after which every call is refused")
	debug := fs.Bool("debug", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	sum, err := toolproxy.ParseTokenSHA(os.Getenv(toolproxy.GateTokenEnv))
	if err != nil {
		return fail("toolproxy gate: %s: %v", toolproxy.GateTokenEnv, err)
	}
	var deadline time.Time
	if *until != "" {
		if deadline, err = time.Parse(time.RFC3339, *until); err != nil {
			return fail("toolproxy gate: -until: %v", err)
		}
	}
	logger := utils.NewStdLogger(os.Stderr, *debug)
	g, err := toolproxy.NewGate(toolproxy.GateConfig{Listen: *listen, Upstream: *upstream, RunID: *run, TokenSHA: sum, Until: deadline, Logger: logger})
	if err != nil {
		return fail("start toolproxy gate: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("remote tool gate listening on %s for run %s until %s", *listen, *run, *until)
	if err := g.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		return fail("toolproxy gate: %v", err)
	}
	return exitOK
}
