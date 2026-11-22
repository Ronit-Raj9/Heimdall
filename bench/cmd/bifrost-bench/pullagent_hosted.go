package main

import (
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
)

// Y11 pull-mode agents: the platform router serves the pull routes for
// every org over the process's broker (handlers/pullagent.go). The agent
// side reads the org from the caller's API token; the run side checks the
// run's signed pull token, whose key (HEIMDALL_PULL_SIGNING_KEY) the
// server and the shared worker share.
func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		cfg := env.base.cfg
		return handlers.NewPullAgentHandler(cfg.Server.Mode, pullagent.Shared(), pullagent.DefaultSigner(), true, cfg.PullPublicOrigin(), nil), nil
	})
}

// withPullOrg is reg with its adapters planning for org: a pull agent's run
// token names the org whose serving process may take its turns.
func withPullOrg(reg *schemas.Registry, a *app, benchRoot, org string) *schemas.Registry {
	if reg == nil || a == nil || a.cfg == nil {
		return reg
	}
	out := *reg
	out.Adapters = adapters.Builtin(adapters.CommandConfig{
		GatewayAgentURL: a.cfg.Gateway.AgentURL, EvalNetwork: a.cfg.Networks.Eval, BenchRoot: benchRoot,
		GatewayPublicURL: a.cfg.Gateway.PublicURL, OrgID: org, PullURL: a.cfg.PullRunURL(),
	})
	return &out
}
