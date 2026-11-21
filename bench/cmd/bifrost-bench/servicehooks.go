package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// serviceHooks fill in an org's handlers.Services (web parity areas). Lanes
// append from init(); each hook may set or replace any field.
var serviceHooks []func(a *app, s *handlers.Services)

// orgInitHooks prepare an org's folders once per runtime build (after
// tenancy.EnsureProject in hosted mode, once at local serve start).
var orgInitHooks []func(p tenancy.OrgPaths) error

// runOrgInitHooks runs every org init hook over p.
func runOrgInitHooks(p tenancy.OrgPaths) error {
	for _, h := range orgInitHooks {
		if err := h(p); err != nil {
			return err
		}
	}
	return nil
}

// localOrgPaths are the local org's folders.
func localOrgPaths(a *app) tenancy.OrgPaths {
	return tenancy.OrgPaths{StoreRoot: a.store.Root(), ProjectRoot: a.cfg.Root}
}

// baseServices builds the default services of one org from its deps.
func baseServices(a *app, deps *server.Deps, orgID string, hosted bool) handlers.Services {
	s := handlers.Services{
		OrgID: orgID, Hosted: hosted,
		Quota:    quota.NewStub(),
		Jobs:     deps.Jobs,
		Launcher: deps.Launcher,
		Index:    deps.Index,
		Suites:   deps.Suites,
		Agents:   deps.Agents,
		Catalog:  deps.Catalog,
		Releases: deps.Releases,
		Analyzer: deps.Analyzer,
	}
	if ps, err := projectfs.Open(a.cfg.Root); err == nil {
		s.Projects = ps
	}
	return s
}

// applyServiceHooks sets deps.Services: the defaults, then every service hook.
func applyServiceHooks(a *app, deps *server.Deps, orgID string, hosted bool) {
	s := baseServices(a, deps, orgID, hosted)
	for _, h := range serviceHooks {
		h(a, &s)
	}
	deps.Services = s
}
