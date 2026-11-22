package main

// Open models on the org's Modal account (lane Z05), hosted wiring: the
// openmodel service over the database (deployments), the keystore (the
// org's Modal token from its platform connection, the per-deployment
// provider key) and the results index (has the evaluation a deployment
// serves ended?), its routes, and its reaper, which stops deployments when
// their evaluation ends or their time limit passes. Local mode never
// reaches this file; its routes answer 503 there (server/server.go).

import (
	"context"
	"errors"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/openmodel"
	"github.com/maximhq/bifrost/bench/server"
)

// openModelService is env's open model service, or nil without a database
// or a keystore master key.
func openModelService(env *hostedEnv) *openmodel.Service {
	conns := platformConnections(env)
	if conns == nil {
		return nil
	}
	d := env.db
	return &openmodel.Service{
		Keys: conns.Keys, Platforms: conns, Deployer: openmodel.ModalDeployer{}, Store: openmodel.NewDBStore(d),
		Ended: func(ctx context.Context, org, expID string) (bool, error) {
			e, err := d.IndexedExperimentByID(ctx, org, expID)
			if errors.Is(err, db.ErrNotFound) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return e.FinishedAt != nil || e.State == "done" || e.State == "rejected", nil
		},
		Logger: env.base.logger,
	}
}

var _ handlers.OpenModels = (*openmodel.Service)(nil)

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var api handlers.OpenModels
		if svc := openModelService(env); svc != nil {
			api = svc
			if env.ctx != nil {
				go svc.Run(env.ctx) // the reaper: nothing stays running on an org's Modal account after its evaluation
			}
		}
		return handlers.NewOpenModelsHandler(env.base.cfg.Server.Mode, api, runnerAudit(env)), nil
	})
}
