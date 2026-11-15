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
	register("toolproxy", "run the per-run tool proxy in front of a run's tool and MCP services (toolproxy bridge: serve a stdio MCP server over HTTP)", runToolProxy)
}

// runToolProxy starts the tool proxy. The environment kernel runs it in the
// container bench-tp-<run id> with the flags below; the tool probe layer
// writes and removes the fault file while it runs.
func runToolProxy(args []string) int {
	if len(args) > 0 && args[0] == "bridge" {
		return runToolBridge(args[1:])
	}
	if len(args) > 0 && args[0] == "gate" { // Remote coverage: the published gate of a remote agent's run (toolproxygate.go)
		return runToolGate(args[1:])
	}
	fs := flag.NewFlagSet("toolproxy", flag.ContinueOnError)
	listen := fs.String("listen", ":9200", "address the agent reaches the proxy on")
	routes := fs.String("routes", "/cfg/routes.json", "routes file: which services are proxied and how")
	fault := fs.String("fault", "/cfg/fault.json", "fault file, read again on every call; absent means no fault")
	ledger := fs.String("ledger", "/ledger/tools.jsonl", "ledger file, one tool call per line")
	debug := fs.Bool("debug", false, "debug logging (one line per call)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	logger := utils.NewStdLogger(os.Stderr, *debug)
	srv, err := toolproxy.New(toolproxy.Config{Listen: *listen, RoutesPath: *routes, FaultPath: *fault, LedgerPath: *ledger, Logger: logger})
	if err != nil {
		return fail("start tool proxy: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("tool proxy listening on %s (routes %s, fault %s, ledger %s)", *listen, *routes, *fault, *ledger)
	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		return fail("tool proxy: %v", err)
	}
	return exitOK
}

// runToolBridge serves a stdio-only MCP server as a streamable HTTP endpoint,
// inside that server's own service image:
//
//	bifrost-bench toolproxy bridge -listen :8080 -path /mcp -- <server argv>
func runToolBridge(args []string) int {
	fs := flag.NewFlagSet("toolproxy bridge", flag.ContinueOnError)
	listen := fs.String("listen", ":8080", "address the MCP endpoint listens on")
	path := fs.String("path", toolproxy.DefaultMCPPath, "MCP endpoint path")
	debug := fs.Bool("debug", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return fail("toolproxy bridge: give the stdio server command after --")
	}
	logger := utils.NewStdLogger(os.Stderr, *debug)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b, err := toolproxy.StartBridge(ctx, argv, *path, logger)
	if err != nil {
		return fail("start bridge: %v", err)
	}
	logger.Info("bridge listening on %s%s for %s", *listen, *path, argv[0])
	if err := toolproxy.ServeBridge(ctx, b, *listen); err != nil && ctx.Err() == nil {
		return fail("bridge: %v", err)
	}
	return exitOK
}
