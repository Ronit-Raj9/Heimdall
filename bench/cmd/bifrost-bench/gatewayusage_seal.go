package main

import (
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
)

// Lane H01: every pipeline and remote coordinator of this process seals the
// heimdall plugin's usage rows of the run's organization into the run folder
// (gateway_usage.json), from the in-memory ingest store.
func init() {
	engine.SetGatewayUsageSource(handlers.SealedGatewayUsage)
}
