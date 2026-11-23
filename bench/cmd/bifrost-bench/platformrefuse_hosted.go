package main

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
)

// Fix wave 3 (N216): the hosted half of the refusal before launch. The
// evaluation create route asks it whether an evaluation goes to a sandbox
// platform and whether that platform can pull Heimdall's images; the "Runs
// go to" setting refuses a platform that cannot.

// platformPoolPrefix starts the runner pool of one evaluation on a sandbox
// platform ("shared/platform/<name>", lane H06's sharedpool.PlatformPool).
const platformPoolPrefix = "shared/platform/"

// targetImagesEnv is the hosted env SetTarget asks platformImagesReady
// with; nil until the hosted areas are built (unit tests).
var targetImagesEnv atomic.Pointer[hostedEnv]

// errPlatformImages refuses a platform without an image registry as where
// runs go: 409 (schemas.ErrExists) with only the plain sentence.
type errPlatformImages struct{ msg string }

func (e errPlatformImages) Error() string { return e.msg }
func (e errPlatformImages) Unwrap() error { return schemas.ErrExists }

// evaluationPlatform is the sandbox platform an evaluation of org whose
// runner pool is pool runs on ("" for the shared worker or an own worker),
// and whether that platform can pull Heimdall's images.
func (w workerTargets) evaluationPlatform(ctx context.Context, env *hostedEnv, org, pool string) (string, bool, error) {
	name := ""
	switch {
	case strings.HasPrefix(pool, platformPoolPrefix):
		name = strings.TrimPrefix(pool, platformPoolPrefix)
	case pool == "":
		pl, wl, err := w.halves(ctx, org)
		if err != nil {
			return "", false, err
		}
		if kind, n, _ := schemas.ParseWorkerTarget(current(pl, wl)); kind == schemas.WorkerTargetPlatform {
			name = n // a platform target runs the shared queue on the platform
		}
	}
	if name == "" {
		return "", false, nil
	}
	return name, platformImagesReady(ctx, env, org, name), nil
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		targetImagesEnv.Store(env)
		t := workerTargetsOf(env)
		if t.platforms != nil {
			handlers.SetPlatformTargetResolver(func(ctx context.Context, org, pool string) (string, bool, error) {
				return t.evaluationPlatform(ctx, env, org, pool)
			})
		}
		return nil, nil
	})
}
