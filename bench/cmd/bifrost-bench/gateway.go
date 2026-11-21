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
	defaultModel         = "claude-sonnet-4-5"      // every anthropic key slot serves it at setup; MintKey adds the run's model
	defaultOpenAIModel   = "gpt-5.4-mini"           // every openai key slot serves it at setup
)

// newGateway builds the Bifrost client and makes sure the provider and its key
// slots exist. The management token comes only from the environment variable
// named in bench.yaml. Each slot's placeholder key value is its name; the
// fault proxy swaps in the real provider key. claims is the fault proxy
// client that also writes the plans (F1: key slot ownership across bench
// processes); nil keeps slots process-local.
func newGateway(ctx context.Context, cfg *lib.Config, docker utils.Docker, logger schemas.Logger, claims bifrost.SlotClaimer) (schemas.Gateway, error) {
	token := os.Getenv(cfg.Gateway.AdminTokenEnv)
	if token == "" {
		return nil, fmt.Errorf("gateway: %s is not set (see deploy/.env.example)", cfg.Gateway.AdminTokenEnv)
	}
	gw, err := bifrost.New(bifrost.Config{
		URL:           cfg.Gateway.URL,
		SetupToken:    token,
		FaultProxyURL: envOr("BENCH_GATEWAY_FAULT_PROXY_URL", defaultFaultProxyURL),
		Providers:     providerSetups(cfg),
		MaxRetries:    0,
		EvalNetwork:   cfg.Networks.Eval,
		HelperImage:   cfg.Images.Helper,
		Docker:        docker,
		Logger:        logger,
		Claims:        claims, // F1: claimed at the fault proxy before a key is minted (gateways/bifrost/slotclaims.go)
	})
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	if err := gw.Setup(ctx); err != nil {
		return nil, fmt.Errorf("gateway setup at %s: %w", cfg.Gateway.URL, err)
	}
	return gw, nil
}

// providerSetups lists the providers the bench configures in Bifrost. Both
// Anthropic and OpenAI go through the fault proxy, so every probe works for
// them; the proxy forwards to the real provider when its key is in the proxy's
// environment and answers with its scripted model otherwise. More providers
// can be added here, proxied or direct.
func providerSetups(cfg *lib.Config) []bifrost.ProviderSetup {
	anth := make([]bifrost.Slot, 0, len(cfg.Gateway.KeySlots))
	for _, s := range cfg.Gateway.KeySlots {
		anth = append(anth, bifrost.Slot{Name: s, Value: s})
	}
	oaNames := cfg.Gateway.OpenAIKeySlots // must match the fault proxy's --openai-slots
	if len(oaNames) == 0 {
		for i := 1; i <= 4; i++ {
			oaNames = append(oaNames, fmt.Sprintf("oa-slot-%d", i))
		}
	}
	oa := make([]bifrost.Slot, 0, len(oaNames))
	for _, n := range oaNames {
		oa = append(oa, bifrost.Slot{Name: n, Value: n})
	}
	return []bifrost.ProviderSetup{
		{Name: "anthropic", Mode: bifrost.ModeProxied, Model: envOr("BENCH_MODEL", defaultModel), Slots: anth},
		{Name: "openai", Mode: bifrost.ModeProxied, Model: envOr("BENCH_OPENAI_MODEL", defaultOpenAIModel), Slots: oa},
	}
}

// envOr returns the environment value of key, or def when it is empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
