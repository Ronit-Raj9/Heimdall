package main

// Fix wave 3 lane H06 (N216 fix 2, N208), runner side: images a sandbox
// platform pulls from the org's own registry.

import (
	"context"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/maximhq/bifrost/bench/utils"
)

// platformRemoteImage is the sandbox driver's RemoteImage for an org's
// platform with an image registry: each local image is pushed to the
// registry with the org's login under the org's Docker policy (d) and
// pulled by digest from there; a Modal client gets the login for its pulls.
// nil without a registry: the driver then refuses an image that has no
// registry digest, with its reason.
func platformRemoteImage(pa *sharedpool.PlatformAssignment, p sandbox.Platform, d sandbox.DockerRunner) func(context.Context, string) (string, error) {
	if pa == nil || pa.Registry == nil || pa.Registry.Registry == "" {
		return nil
	}
	login := sandbox.ImageRegistry{Registry: pa.Registry.Registry, Username: pa.Registry.Username, Token: pa.Registry.Token}
	if m, ok := p.(*sandbox.Modal); ok {
		m.UseRegistry(login)
	}
	return (&sandbox.RegistryImages{Login: login, Docker: d, HostCheck: registryHostCheck}).Remote
}

// registryHostCheck is checked before every push to an org's registry; nil
// is sandbox.PublicRegistryHost. Tests replace it so they need no DNS.
var registryHostCheck func(ctx context.Context, host string) error

// platformPoolEnv is what a platform pool's worker shares with its org's
// main worker loop (serveOrg): the org, the main worker's name, the
// planning root, the org's Docker policy and the project refresh.
type platformPoolEnv struct {
	org, name, planRoot string
	docker              utils.Docker
	refresh             func(context.Context) error
}

// servePlatformPool runs the worker of one platform that takes single
// evaluations (N208): its own registry on the platform, its own runner
// token, scoped to the platform's pool (sharedpool.PlatformPool), so it
// leases only the evaluations sent there. It ends with ctx (the org loop).
func servePlatformPool(ctx context.Context, h *sharedHost, oa *app, reg *schemas.Registry, pc sharedpool.PlatformClaim, slots int, e platformPoolEnv) {
	a := h.base
	if pc.Platform == nil || pc.Token == "" {
		return
	}
	pr, err := sandboxRegistry(ctx, oa, reg, pc.Platform)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Error("shared runner: platform for single evaluations not usable; its evaluations wait", "org", e.org, "platform", pc.Platform.Name, "error", err.Error())
		}
		return
	}
	name := e.name + "-" + pc.Platform.Name
	if len(name) > 128 {
		name = name[:128]
	}
	labels := append(h.sharedWorkerLabels(), "platform="+pc.Platform.Name)
	plan := engine.NewPlanner(pr, e.planRoot, oa.logger, sharedPlannerOptions(e.docker, nil)...).Plan
	w, err := engine.NewWorker(pr, engine.NewPipeline(pr, e.planRoot, oa.logger),
		sharedEnsureImages(oa, pr, e.planRoot, e.refresh, plan),
		engine.WorkerOptions{Server: h.f.server, Token: pc.Token, WorkerID: name, Host: h.host, Version: benchVersion,
			Slots: h.sharedWorkerSlots(slots), Logger: oa.logger, Pool: sharedpool.PlatformPool(pc.Platform.Name), Labels: labels,
			HostSlot: h.orgHostSlot(e.org, slots), Sync: e.refresh})
	if err != nil {
		a.logger.Error("shared runner: platform worker not started", "org", e.org, "platform", pc.Platform.Name, "error", err.Error())
		return
	}
	a.logger.Info("shared runner: serving single evaluations on a platform", "org", e.org, "platform", pc.Platform.Name)
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		a.logger.Error("shared runner: platform worker stopped", "org", e.org, "platform", pc.Platform.Name, "error", err.Error())
	}
}
