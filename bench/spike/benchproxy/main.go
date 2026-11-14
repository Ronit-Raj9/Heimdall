// Command benchproxy runs the bench fault proxy (package bench/faultproxy)
// with its built-in provider table and default key slots, for the gateway
// integration tests in the spike compose stack. It needs no bench config:
// FP_LEDGER is the ledger path, FP_CONTROL_TOKEN the control token,
// FP_PROVIDERS an optional provider table file, and FP_MODE=scripted (the
// default) answers every scripted provider from the scripted model even when
// a provider key is in the environment. FP_MODE=real reads the keys.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "benchproxy:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := faultproxy.Config{
		Listen:        ":9100",
		ControlListen: "0.0.0.0:9101",
		ControlToken:  os.Getenv("FP_CONTROL_TOKEN"),
		LedgerPath:    envOr("FP_LEDGER", "/ledger/ledger.jsonl"),
		Logger:        schemas.NoopLogger{},
	}
	if envOr("FP_MODE", "scripted") != "real" {
		cfg.Getenv = func(string) string { return "" }
	}
	if file := os.Getenv("FP_PROVIDERS"); file != "" {
		specs, err := faultproxy.LoadProviders(file)
		if err != nil {
			return err
		}
		cfg.Providers = specs
	}
	srv, err := faultproxy.NewServer(cfg)
	if err != nil {
		return err
	}
	defer srv.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(os.Stderr, "benchproxy listening on :9100 (control 0.0.0.0:9101)")
	return srv.ListenAndServe(ctx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
