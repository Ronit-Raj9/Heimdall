package main

// Runners area (P5): the hosted wiring of runners (docs/runners.md). One
// coordinator per organization (engine.RemoteHub), built over the org's own
// store when tenancy builds the org's runtime; runner tokens verified for
// the auth middleware; the runner admin routes as a platform area. Local
// mode never reaches this file: it keeps BENCH_WORKER_TOKEN (worker.go).

import (
	"context"
	"github.com/maximhq/bifrost/bench/selfmetrics"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/auth"
	runnerdeploy "github.com/maximhq/bifrost/bench/deploy/runner"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leasebook"
	"github.com/maximhq/bifrost/bench/runners"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// runnerRepoEnv names the git repository runners clone (install.sh --repo),
// shown in the install command of a new runner token. Set, runners clone it
// instead of downloading the project from this server; unset (the default),
// the install script downloads /downloads/bench-<commit>.tar.gz and the
// binary from this server.
const runnerRepoEnv = "HEIMDALL_RUNNER_REPO"

// installCommitEnv names the commit a server built without version control
// information runs (the hosted image builds from a tree with no .git). The
// runner install script downloads /downloads/bench-<commit>.tar.gz and the
// CLI binary of that commit from this server.
const installCommitEnv = "HEIMDALL_BUILD_COMMIT"

// installRevision is the commit runners install (install.sh --ref): the
// build's own revision, else HEIMDALL_BUILD_COMMIT when it is a plain
// commit id, else empty.
func installRevision() string {
	if rev := buildRevision(); rev != "" {
		return rev
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv(installCommitEnv)))
	if len(v) < 7 || len(v) > 40 || strings.Trim(v, "0123456789abcdef") != "" {
		return ""
	}
	return v
}

// buildRevision is the git commit this binary was built from, when the
// build recorded it: a runner must run the same commit (install.sh --ref).
func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty {
		return "" // a modified tree has no commit a runner could check out
	}
	return rev
}

// hostedRunners is the runners area of one hosted server.
type hostedRunners struct {
	hub *engine.RemoteHub
	svc *runners.Service
}

// runnerEnvs holds the runners area of each hosted environment (one per
// serve process; tests build several).
var runnerEnvs sync.Map // *hostedEnv -> *hostedRunners

// runnersOf returns env's runners area, building it on first use and
// registering its runner token verifier with the auth middleware.
func runnersOf(env *hostedEnv) *hostedRunners {
	if v, ok := runnerEnvs.Load(env); ok {
		return v.(*hostedRunners)
	}
	svc := runners.New(env.db, runners.Options{PublicURL: env.base.cfg.Auth.PublicURL, Logger: env.base.logger,
		Repo: os.Getenv(runnerRepoEnv), Ref: installRevision()})
	ropts := engine.RemoteOptions{Logger: env.base.logger, Book: svc, Meter: billingMeter{env: env}} // Y16: shared worker minutes (billing_hosted.go)
	if env.db != nil {
		ropts.Leases = &leasebook.Book{DB: env.db} // Z11 durable leases: a restart keeps runs in flight (engine/remotebook.go)
	}
	hub := engine.NewRemoteHub(ropts)
	svc.SetLive(hub)
	v, loaded := runnerEnvs.LoadOrStore(env, &hostedRunners{hub: hub, svc: svc})
	rs := v.(*hostedRunners)
	if !loaded {
		auth.RegisterRunnerVerifier(rs.svc.VerifyPrincipal)
		registerCoordinatorMetrics(selfmetrics.Default, rs.hub.Coordinators) // N9 (servemetrics.go)
	}
	return rs
}

// hostedOrgCoordinator is the coordinator of one hosted org, over that org's
// store (runs/orgs/<org_id>/, staging in its .remote folder). The launcher
// of the org attaches it to every run with RemoteOnly set: strangers' agents
// never run on the hosted server.
func hostedOrgCoordinator(env *hostedEnv, oa *app) (*engine.Coordinator, error) {
	return runnersOf(env).hub.For(oa.orgID, oa.reg)
}

// hostedOrgLauncher is the default orgLauncher: the org's launcher with its
// coordinator attached and no local slot. The jobs area may replace it; it
// then sets launcher.coord from hostedOrgCoordinator and remoteOnly.
func hostedOrgLauncher(env *hostedEnv, oa *app, ix *store.Index) (handlers.Launcher, handlers.WorkerAPI) {
	l := newLauncher(oa, ix)
	l.org = oa.orgID
	l.remoteOnly = true
	c, err := hostedOrgCoordinator(env, oa)
	if err != nil {
		env.base.logger.Error("runners: no coordinator for the organization; its experiments can not run", "org", oa.orgID, "error", err.Error())
		return l, nil
	}
	l.coord = c
	return l, c
}

// runnerAudit writes the runner admin routes' audit rows.
func runnerAudit(env *hostedEnv) handlers.AuditFunc {
	return func(ctx *fasthttp.RequestCtx, orgID, action, targetType, targetID string, details map[string]any) {
		if env.audit == nil {
			return
		}
		_ = env.audit.Audit(context.WithoutCancel(ctx), tenancy.EntryFor(ctx, orgID, action, targetType, targetID, details)) // loggedAudit logs a failure
	}
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		rs := runnersOf(env)
		return handlers.NewRunnersHandler(env.base.cfg.Server.Mode, rs.svc, runnerAudit(env)).
			WithInstallScript(runnerdeploy.InstallScript).
			WithInstallDefaults(env.base.cfg.Auth.PublicURL, os.Getenv(runnerRepoEnv), installRevision()), nil
	})
	if orgLauncher == nil {
		orgLauncher = hostedOrgLauncher
	}
}
