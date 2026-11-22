package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

// Identity (Wave A): email verification and the signup mode setting. Like
// the auth area, test environments without the auth service get no routes.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env.auth == nil {
			return nil, nil
		}
		return handlers.NewIdentityHandler(env.base.cfg.Server.Mode, env.auth), nil
	})
}
