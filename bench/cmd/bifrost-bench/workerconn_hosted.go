package main

// Bring your own worker (package byoworker, FIX_BRIEF decision 6), hosted
// wiring: the worker connections service over the database, the keystore
// and the runners area, its platform routes, and the evaluation hook that
// routes an evaluation to the worker it names (or the org's default).
// Local mode never reaches this file.

import (
	"os"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/byoworker"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/server"
)

// runnerImageEnv names the runner image the platforms that run an image
// (Fly Machines without an image setting, Kubernetes) start.
const runnerImageEnv = "HEIMDALL_RUNNER_IMAGE"

// workerConns holds the worker connections service of each hosted
// environment; nil when the server has no master key.
var workerConns sync.Map // *hostedEnv -> *byoworker.Service

// workerDrivers are the platform drivers bring-your-own-worker offers.
// Lane X19a adds Modal, Daytona and E2B here.
func workerDrivers() byoworker.Registry {
	return byoworker.NewRegistry(byoworker.Runloop{}, byoworker.Fly{}, byoworker.Kubernetes{})
}

// workerConnsOf returns env's worker connections service, or nil.
func workerConnsOf(env *hostedEnv) *byoworker.Service {
	if v, ok := workerConns.Load(env); ok {
		s, _ := v.(*byoworker.Service)
		return s
	}
	var svc *byoworker.Service
	if env.db != nil {
		if mk, err := keystore.MasterKey(); err != nil {
			env.base.logger.Warn("bring your own worker is off: no master key for the stored credentials", "error", err.Error())
		} else if keys, err := keystore.Open(env.db, mk); err != nil {
			env.base.logger.Warn("bring your own worker is off: keystore", "error", err.Error())
		} else {
			rs := runnersOf(env).svc
			svc = byoworker.New(env.db, byoworker.Options{Drivers: workerDrivers(), Keys: keys, Minter: rs, Runners: rs,
				PublicURL: env.base.cfg.Auth.PublicURL, RunnerImage: strings.TrimSpace(os.Getenv(runnerImageEnv))})
		}
	}
	v, _ := workerConns.LoadOrStore(env, svc)
	s, _ := v.(*byoworker.Service)
	if s != nil {
		handlers.SetEvaluationPoolResolver(s.EvaluationPool)
	}
	return s
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		var api handlers.WorkerConnAdmin
		if s := workerConnsOf(env); s != nil {
			api = s
		}
		return handlers.NewWorkerConnsHandler(env.base.cfg.Server.Mode, api,
			handlers.WorkerConnErrors{Invalid: byoworker.ErrInvalid, NotFound: byoworker.ErrNotFound, Refused: byoworker.ErrRefused},
			runnerAudit(env)), nil
	})
}
