package main

// Where runs go (FIX_BRIEF decision 6, integration of lanes X19a and X20):
// one per-org setting over the cloud sandbox platforms (a fresh sandbox per
// run, sandbox.Connections) and the org's own workers (long-lived runners,
// byoworker.Service). The platform default and the worker default are the
// stored halves of it; setting one clears the other, through the new
// /workers/target routes and through the old /worker-platforms/default and
// /workers/default routes alike, so the two can never disagree. Local mode
// never reaches this file.

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
)

// workerPlatformStore is the platform half (sandbox.Connections).
type workerPlatformStore interface {
	List(ctx context.Context, org string) (*sandbox.ConnectionsList, error)
	SetDefault(ctx context.Context, org, name, actor string) (*sandbox.ConnectionsList, error)
}

// workerPoolStore is the own worker half (byoworker.Service).
type workerPoolStore interface {
	List(ctx context.Context, orgID string) (*schemas.WorkerConnectionsList, error)
	SetDefaultWorker(ctx context.Context, orgID, actor, pool string) (string, error)
	EvaluationPool(ctx context.Context, orgID, worker string) (string, error)
}

// workerTargets is the one setting. Either half may be nil (a server
// without platforms or without own workers).
type workerTargets struct {
	platforms workerPlatformStore
	workers   workerPoolStore
	// runners fills each choice's runs at once (N93); nil leaves them out.
	runners runnerLister
}

func (w workerTargets) halves(ctx context.Context, org string) (*sandbox.ConnectionsList, *schemas.WorkerConnectionsList, error) {
	var pl *sandbox.ConnectionsList
	var wl *schemas.WorkerConnectionsList
	var err error
	if w.platforms != nil {
		if pl, err = w.platforms.List(ctx, org); err != nil {
			return nil, nil, fmt.Errorf("list worker platforms: %w", err)
		}
	}
	if w.workers != nil {
		if wl, err = w.workers.List(ctx, org); err != nil {
			return nil, nil, fmt.Errorf("list workers: %w", err)
		}
	}
	return pl, wl, nil
}

// current is the target the stored halves give. An own worker default wins
// over a platform default (only a store written before the one setting
// could hold both): evaluations go to the worker's pool.
func current(pl *sandbox.ConnectionsList, wl *schemas.WorkerConnectionsList) string {
	if wl != nil && wl.DefaultWorker != "" {
		return schemas.WorkerTargetOf(schemas.WorkerTargetWorker, wl.DefaultWorker)
	}
	if pl != nil && pl.Default != "" {
		return schemas.WorkerTargetOf(schemas.WorkerTargetPlatform, pl.Default)
	}
	return schemas.WorkerTargetShared
}

// Target implements handlers.WorkerTargets.
func (w workerTargets) Target(ctx context.Context, org string) (*schemas.WorkerTargetView, error) {
	pl, wl, err := w.halves(ctx, org)
	if err != nil {
		return nil, err
	}
	v := targetView(pl, wl)
	w.fillSlots(ctx, org, v)
	return v, nil
}

// fillSlots sets how many runs each worker choice runs at once from the
// org's runner rows (N93, the same rule as Services.WorkerSlots); a
// sandbox platform is not bounded. A failed lookup leaves them out.
func (w workerTargets) fillSlots(ctx context.Context, org string, v *schemas.WorkerTargetView) {
	if w.runners == nil {
		return
	}
	rs, err := w.runners.ListRunners(ctx, org)
	if err != nil {
		return
	}
	now := time.Now()
	for i := range v.Choices {
		if pool, ok := workerPoolOf(v.Choices[i].Target); ok {
			v.Choices[i].Slots = poolSlots(rs, pool, now)
		}
	}
}

// targetView lists the choices, the current one first among its kind.
func targetView(pl *sandbox.ConnectionsList, wl *schemas.WorkerConnectionsList) *schemas.WorkerTargetView {
	cur := current(pl, wl)
	curKind, _, _ := schemas.ParseWorkerTarget(cur)
	v := &schemas.WorkerTargetView{Target: cur, Choices: []schemas.WorkerTargetChoice{}}
	shared := schemas.WorkerTargetChoice{Target: schemas.WorkerTargetShared, Kind: schemas.WorkerTargetShared, Name: schemas.WorkerTargetShared,
		Title: "Shared worker", Current: cur == schemas.WorkerTargetShared, PerEvaluation: curKind != schemas.WorkerTargetPlatform}
	if !shared.PerEvaluation {
		shared.Note = "while a sandbox platform is where runs go, the shared worker runs every run there"
	}
	v.Choices = append(v.Choices, shared)
	if pl != nil {
		for _, p := range pl.Platforms {
			if !p.Connected {
				continue
			}
			t := schemas.WorkerTargetOf(schemas.WorkerTargetPlatform, p.Name)
			c := schemas.WorkerTargetChoice{Target: t, Kind: schemas.WorkerTargetPlatform, Name: p.Name, Title: p.Title, Status: p.Status,
				Current: t == cur, PerEvaluation: t == cur}
			if !c.PerEvaluation {
				c.Note = "a sandbox platform takes an evaluation only while it is where runs go"
			}
			v.Choices = append(v.Choices, c)
		}
	}
	if wl != nil {
		for _, c := range wl.Connections {
			t := schemas.WorkerTargetOf(schemas.WorkerTargetWorker, c.Pool)
			v.Choices = append(v.Choices, schemas.WorkerTargetChoice{Target: t, Kind: schemas.WorkerTargetWorker, Name: c.Pool,
				Title: c.Name + " (" + c.PlatformTitle + ")", Status: c.Status, Current: t == cur, PerEvaluation: true})
		}
	}
	return v
}

// SetTarget implements handlers.WorkerTargets: the chosen half is set and
// the other one cleared.
func (w workerTargets) SetTarget(ctx context.Context, org, actor, target string) (*schemas.WorkerTargetView, error) {
	kind, name, err := schemas.ParseWorkerTarget(target)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", handlers.ErrWorkerTarget, err)
	}
	pl, wl, err := w.halves(ctx, org)
	if err != nil {
		return nil, err
	}
	switch kind {
	case schemas.WorkerTargetPlatform:
		if w.platforms == nil || !platformConnected(pl, name) {
			return nil, fmt.Errorf("%w: %s is not a connected sandbox platform (connect it on the Workers page first)", handlers.ErrWorkerTarget, name)
		}
	case schemas.WorkerTargetWorker:
		if w.workers == nil || !workerConnected(wl, name) {
			return nil, fmt.Errorf("%w: no worker named %s (connect it on the Workers page first)", handlers.ErrWorkerTarget, name)
		}
	}
	platform, pool := "", ""
	switch kind {
	case schemas.WorkerTargetPlatform:
		platform = name
	case schemas.WorkerTargetWorker:
		pool = name
	}
	// Clear first, then set, so a failure half way never leaves both on.
	if w.workers != nil && pool == "" {
		if _, err := w.workers.SetDefaultWorker(ctx, org, actor, ""); err != nil {
			return nil, err
		}
	}
	if w.platforms != nil && platform == "" && pl != nil && pl.Default != "" {
		if _, err := w.platforms.SetDefault(ctx, org, "", actor); err != nil {
			return nil, err
		}
	}
	if platform != "" {
		if _, err := w.platforms.SetDefault(ctx, org, platform, actor); err != nil {
			return nil, err
		}
	}
	if pool != "" {
		if _, err := w.workers.SetDefaultWorker(ctx, org, actor, pool); err != nil {
			return nil, err
		}
	}
	return w.Target(ctx, org)
}

func platformConnected(pl *sandbox.ConnectionsList, name string) bool {
	if pl == nil {
		return false
	}
	for _, p := range pl.Platforms {
		if p.Name == name && p.Connected {
			return true
		}
	}
	return false
}

func workerConnected(wl *schemas.WorkerConnectionsList, pool string) bool {
	if wl == nil {
		return false
	}
	for _, c := range wl.Connections {
		if c.Pool == pool {
			return true
		}
	}
	return false
}

// EvaluationPool routes an evaluation that names worker (a target, a
// worker's pool, "shared" or "" for where runs go) to a runner pool; ""
// is the shared worker's queue, which a platform target's runs also use.
func (w workerTargets) EvaluationPool(ctx context.Context, org, worker string) (string, error) {
	pl, wl, err := w.halves(ctx, org)
	if err != nil {
		return "", err
	}
	cur := current(pl, wl)
	curKind, curName, _ := schemas.ParseWorkerTarget(cur)
	if worker == "" {
		if curKind == schemas.WorkerTargetWorker {
			return curName, nil
		}
		return "", nil
	}
	kind, name, perr := schemas.ParseWorkerTarget(worker)
	if perr != nil { // a bare pool name: one of the org's own workers
		kind, name = schemas.WorkerTargetWorker, worker
	}
	switch kind {
	case schemas.WorkerTargetShared:
		if curKind == schemas.WorkerTargetPlatform {
			return "", fmt.Errorf("the shared worker runs this organization's runs on %s while it is where runs go: pick it, or change where runs go on the Workers page", curName)
		}
		return "", nil
	case schemas.WorkerTargetPlatform:
		if cur != schemas.WorkerTargetOf(kind, name) {
			return "", fmt.Errorf("sandbox platform %s takes an evaluation only while it is where runs go: choose it on the Workers page", name)
		}
		return "", nil
	}
	if w.workers == nil {
		return "", fmt.Errorf("no worker named %s", name)
	}
	return w.workers.EvaluationPool(ctx, org, name)
}

// aliasedPlatforms is the platform settings API with its old default route
// setting the one target (the own worker default is cleared).
type aliasedPlatforms struct {
	*sandbox.Connections
	t workerTargets
}

func (a aliasedPlatforms) SetDefault(ctx context.Context, org, name, actor string) (*sandbox.ConnectionsList, error) {
	if _, err := a.t.SetTarget(ctx, org, actor, schemas.WorkerTargetOf(schemas.WorkerTargetPlatform, name)); err != nil {
		return nil, err
	}
	return a.Connections.List(ctx, org)
}

// aliasedWorkers is the own worker API with its old default route setting
// the one target (the platform default is cleared).
type aliasedWorkers struct {
	handlers.WorkerConnAdmin
	t workerTargets
}

func (a aliasedWorkers) SetDefaultWorker(ctx context.Context, orgID, actor, pool string) (string, error) {
	kind := schemas.WorkerTargetWorker
	if pool == "" || pool == schemas.DefaultRunnerPool || pool == schemas.SharedRunnerPool {
		kind, pool = schemas.WorkerTargetShared, ""
	}
	v, err := a.t.SetTarget(ctx, orgID, actor, schemas.WorkerTargetOf(kind, pool))
	if err != nil {
		return "", err
	}
	if k, name, _ := schemas.ParseWorkerTarget(v.Target); k == schemas.WorkerTargetWorker {
		return name, nil
	}
	return "", nil
}

// workerTargetsOf is env's one setting over both halves.
func workerTargetsOf(env *hostedEnv) workerTargets {
	var t workerTargets
	if c := platformConnections(env); c != nil {
		t.platforms = c
	}
	if s := workerConnsOf(env); s != nil {
		t.workers = s
	}
	if env.db != nil {
		t.runners = runnersOf(env).svc
	}
	return t
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		t := workerTargetsOf(env)
		var api handlers.WorkerTargets
		if t.platforms != nil || t.workers != nil {
			api = t
			handlers.SetEvaluationPoolResolver(t.EvaluationPool)
		}
		return handlers.NewWorkerTargetHandler(env.base.cfg.Server.Mode, api, runnerAudit(env)), nil
	})
}
