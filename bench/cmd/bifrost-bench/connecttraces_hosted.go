package main

import (
	"context"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// Lane Z15: the hosted per-org OTLP traces endpoint. It takes the org's
// inbox ingest tokens and keeps the spans inference reads in the org's
// project folder (.heimdall/connect/traces).
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var tokens inboxtoken.Store
		tokensErr := "no database"
		if env.db != nil {
			tokens, tokensErr = inboxtoken.Open(env.db), ""
		}
		root := func(_ context.Context, org tenancy.Org) (string, error) {
			p, err := env.layout.Paths(org.ID)
			if err != nil {
				return "", err
			}
			if err := tenancy.EnsureProject(p, seedBenchmarks(env.base.cfg)); err != nil {
				return "", err
			}
			return p.ProjectRoot, nil
		}
		return handlers.NewOTLPIngestHandler(env.base.cfg.Server.Mode, tokens, tokensErr, env.dir, root), nil
	})
}
