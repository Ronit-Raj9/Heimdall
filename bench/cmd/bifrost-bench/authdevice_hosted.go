package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
)

// Device login (RFC 8628, lane Z02): the server side of `heimdall login`.
// Like the auth area, test environments without the auth service get no routes.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env.auth == nil {
			return nil, nil
		}
		return handlers.NewDeviceHandler(env.base.cfg.Server.Mode, env.auth), nil
	})
}
