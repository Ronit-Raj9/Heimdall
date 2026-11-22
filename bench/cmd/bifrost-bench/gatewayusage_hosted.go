package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

// Lane C03: the heimdall Bifrost plugin posts usage rows to the platform
// router with a shared secret; there is no org session on that call.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return handlers.NewGatewayUsageIngest(env.base.cfg.Server.Mode), nil
	})
}
