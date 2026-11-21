package main

// Bring your own worker (lane X19a), hosted wiring: each org's cloud sandbox
// connections (Modal, Daytona, E2B) over the keystore, their settings
// routes, and the org's default platform in the shared runner's
// assignments (sharedpool_hosted.go). Local mode never reaches this file.

import (
	"context"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

// platformConnections is env's connections service, or nil without a
// database or a keystore master key.
func platformConnections(env *hostedEnv) *sandbox.Connections {
	if env.db == nil {
		return nil
	}
	mk, err := keystore.MasterKey()
	if err != nil {
		return nil
	}
	keys, err := keystore.Open(env.db, mk)
	if err != nil {
		env.base.logger.Warn("worker platforms are off: keystore", "error", err.Error())
		return nil
	}
	return &sandbox.Connections{Keys: keys}
}

// platformAssignment resolves an org's default platform for the shared
// runner's assignment.
func platformAssignment(c *sandbox.Connections) func(context.Context, string) (*sharedpool.PlatformAssignment, error) {
	if c == nil {
		return nil
	}
	return func(ctx context.Context, org string) (*sharedpool.PlatformAssignment, error) {
		r, err := c.Resolve(ctx, org)
		if err != nil || r == nil {
			return nil, err
		}
		return &sharedpool.PlatformAssignment{Name: r.Platform, Token: r.Credentials.Token, Endpoint: r.Credentials.Endpoint}, nil
	}
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var api handlers.WorkerPlatforms
		if c := platformConnections(env); c != nil {
			api = c
		}
		return handlers.NewWorkerPlatformsHandler(env.base.cfg.Server.Mode, api, runnerAudit(env)), nil
	})
}
