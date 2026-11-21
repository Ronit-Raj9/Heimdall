package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

// Account safety (L22): owner reset links and the reset page's API. Like
// the auth area, test environments without the auth service get no routes.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env.auth == nil {
			return nil, nil
		}
		return handlers.NewAccountSafetyHandler(env.base.cfg.Server.Mode, env.auth), nil
	})
}
