package main

import (
	"context"
	"fmt"
	"os"

	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Defaults for the gateway wiring that bench.yaml does not carry yet.
const (
	defaultFaultProxyURL = "http://faultproxy:9100" // the fault proxy as Bifrost sees it
	defaultModel         = "claude-sonnet-4-5"      // every key slot serves it at setup; MintKey adds the run's model
)

// newGateway builds the Bifrost client and makes sure the provider and its key
// slots exist. The management token comes only from the environment variable
// named in bench.yaml. Each slot's placeholder key value is its name; the
// fault proxy swaps in the real provider key.
func newGateway(ctx context.Context, cfg *lib.Config, docker utils.Docker, logger schemas.Logger) (schemas.Gateway, error) {
	token := os.Getenv(cfg.Gateway.AdminTokenEnv)
	if token == "" {
		return nil, fmt.Errorf("gateway: %s is not set (see deploy/.env.example)", cfg.Gateway.AdminTokenEnv)
	}
	slots := make([]bifrost.Slot, 0, len(cfg.Gateway.KeySlots))
	for _, s := range cfg.Gateway.KeySlots {
		slots = append(slots, bifrost.Slot{Name: s, Value: s})
	}
	model := envOr("BENCH_MODEL", defaultModel)
	gw, err := bifrost.New(bifrost.Config{
		URL:           cfg.Gateway.URL,
		SetupToken:    token,
		Provider:      cfg.Gateway.Provider,
		FaultProxyURL: envOr("BENCH_GATEWAY_FAULT_PROXY_URL", defaultFaultProxyURL),
		Model:         model,
		Slots:         slots,
		MaxRetries:    0,
		EvalNetwork:   cfg.Networks.Eval,
		HelperImage:   cfg.Images.Helper,
		Docker:        docker,
		Logger:        logger,
	})
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	if err := gw.Setup(ctx); err != nil {
		return nil, fmt.Errorf("gateway setup at %s: %w", cfg.Gateway.URL, err)
	}
	return gw, nil
}

// envOr returns the environment value of key, or def when it is empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
