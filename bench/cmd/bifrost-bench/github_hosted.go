package main

import (
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/server"
)

// GitHub connect area (lane L07): the repo-scope GitHub connection of an
// org, served on the platform router. The routes are always registered;
// without an OAuth app or a master key they answer 503 saying what to set.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var (
			keys    keystore.Store
			keysErr string
		)
		if master, err := keystore.MasterKey(); err != nil {
			keysErr = err.Error()
		} else if keys, err = keystore.Open(env.db, master); err != nil {
			keys, keysErr = nil, err.Error()
		}
		return handlers.NewGitHubHandler(env.base.cfg.Server.Mode, auth.NewGitHubRepoConnector(env.authCfg), keys, keysErr, env.audit), nil
	})
}
