package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/server"
)

// Issues to benchmark (lane G01): the scan dry run on the platform router,
// reading GitHub with the org's repo connection (or a token in the request).
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
		apiURL := ""
		if env.authCfg.GitHub != nil {
			apiURL = env.authCfg.GitHub.APIURL
		}
		return handlers.NewIssueBenchHandler(env.base.cfg.Server.Mode, keys, keysErr, apiURL), nil
	})
}
