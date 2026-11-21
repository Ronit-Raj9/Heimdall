package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("proxy", "run the fault proxy that sits behind Bifrost's anthropic provider", runProxy)
}

// runProxy starts the fault proxy. The real provider key comes only from the
// environment (ANTHROPIC_API_KEY) and the control token from
// BENCH_FAULTPROXY_TOKEN, so neither appears in a process listing.
func runProxy(args []string) int {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	listen := fs.String("listen", envOr("BENCH_FAULTPROXY_LISTEN", ":9100"), "data-plane address Bifrost's base_url points at")
	control := fs.String("control", envOr("BENCH_FAULTPROXY_CONTROL_LISTEN", "127.0.0.1:9101"), "control API address")
	upstream := fs.String("upstream", envOr("BENCH_FAULTPROXY_UPSTREAM", ""), `anthropic only: "scripted" or the provider URL, e.g. https://api.anthropic.com; empty reads each provider's key from the environment`)
	scriptedFallback := fs.Bool("scripted-fallback", envBool("BENCH_FAULTPROXY_SCRIPTED_FALLBACK"), "answer anthropic and openai calls from the scripted model when their key is empty (local development only; never on a hosted stack)")
	ledger := fs.String("ledger", envOr("BENCH_FAULTPROXY_LEDGER", "faultproxy.ledger.jsonl"), "ledger file path")
	_ = fs.String("config", "", "bench config (accepted for compose; the proxy reads only its flags and environment)")
	slots := fs.String("slots", "fp-slot-1,fp-slot-2,fp-slot-3,fp-slot-4", "comma-separated key slots; each slot's placeholder key value equals its name")
	openaiSlots := fs.String("openai-slots", envOr("BENCH_FAULTPROXY_OPENAI_SLOTS", ""), "comma-separated key slots of the openai provider (empty: oa-slot-1..4); keep in step with gateway.openai_key_slots")
	debug := fs.Bool("debug", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	logger := utils.NewStdLogger(os.Stderr, *debug)
	cfg := faultproxy.Config{
		Listen:        *listen,
		ControlListen: *control,
		ControlToken:  os.Getenv("BENCH_FAULTPROXY_TOKEN"),
		Upstream:      *upstream,
		UpstreamKey:   os.Getenv("ANTHROPIC_API_KEY"),
		LedgerPath:    *ledger,
		Logger:        logger,

		ScriptedFallback: *scriptedFallback,
	}
	if file := os.Getenv("BENCH_FAULTPROXY_PROVIDERS"); file != "" { // X8: the provider table file the compose files pass (faultproxy.LoadProviders)
		list, err := faultproxy.LoadProviders(file)
		if err != nil {
			return fail("%v", err)
		}
		cfg.Providers = list
	}
	cfg.Slots = proxySlots(*slots, *openaiSlots)
	if *upstream != "" && *upstream != "scripted" && cfg.UpstreamKey == "" {
		return fail("upstream %s needs ANTHROPIC_API_KEY in the environment", *upstream)
	}
	srv, err := faultproxy.NewServer(cfg)
	if err != nil {
		return fail("start fault proxy: %v", err)
	}
	if cfg.ControlToken == "" {
		fmt.Fprintf(os.Stderr, "control token (set BENCH_FAULTPROXY_TOKEN to fix it): %s\n", srv.ControlToken())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("fault proxy listening on %s (control %s, upstream %s, %d slots)", *listen, *control, *upstream, len(cfg.Slots))
	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		return fail("fault proxy: %v", err)
	}
	return exitOK
}

// envBool reads a boolean environment variable: 1, true, yes or on.
func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// proxySlots turns the --slots (anthropic) and --openai-slots lists into the
// proxy's slot table. An empty openai list leaves openai on its default
// oa-slot-1..4 (faultproxy.DefaultSlots); a non-empty one replaces them.
func proxySlots(anthropic, openai string) []faultproxy.SlotConfig {
	var out []faultproxy.SlotConfig
	for _, s := range strings.Split(anthropic, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, faultproxy.SlotConfig{Name: s, Auth: s})
		}
	}
	for _, s := range strings.Split(openai, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, faultproxy.SlotConfig{Name: s, Auth: s, Provider: faultproxy.ProviderOpenAI})
		}
	}
	return out
}
