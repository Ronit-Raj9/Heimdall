package main

// Fix wave 3 lane H06 (N216 fix 2), hosted wiring: a sandbox platform can
// pull Heimdall's images once the org saved an image registry on its card
// (sandbox.Connections.SaveRegistry). platformImagesReady (platformimages.go)
// answers from that login; until it is saved, runs to the platform are
// refused before launch.

import (
	"context"

	"github.com/maximhq/bifrost/bench/sandbox"
)

// platformRegistryConns is env's platform connections (tests replace it).
var platformRegistryConns = platformConnections

func init() {
	platformImagesReady = func(ctx context.Context, env *hostedEnv, org, name string) bool {
		c := platformRegistryConns(env)
		if c == nil {
			return false
		}
		return registrySaved(ctx, c, org, name)
	}
}

// registrySaved reports whether org saved an image registry for platform
// name.
func registrySaved(ctx context.Context, c *sandbox.Connections, org, name string) bool {
	r, err := c.RegistryOf(ctx, org, name)
	return err == nil && r != nil
}
