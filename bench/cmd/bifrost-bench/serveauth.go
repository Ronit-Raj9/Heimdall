package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

// Auth area (P2, docs/auth.md): the /api/bench/auth/ routes of hosted mode,
// served directly on the platform router behind the auth middleware. Test
// environments built without the auth service (a fake middleware) get no
// auth routes.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env.auth == nil {
			return nil, nil
		}
		return handlers.NewAuthHandler(env.base.cfg.Server.Mode, env.auth), nil
	})
}
