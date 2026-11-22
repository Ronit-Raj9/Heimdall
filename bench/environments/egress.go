package environments

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Recorded internet (docs/egress.md). A run with a service of role egress
// hands the agent and the scripted controls the proxy variables, with every
// host the plan itself names (model gateway, tool proxy, other services) in
// NO_PROXY. The experiment's egress mode, when set, overrides the service's
// own (replay by default); only live-allowlisted gives the service a route
// out, on a network of its own that the agent never joins.

// egressSpec is the run's egress service, or nil.
func egressSpec(rc *schemas.RunContext) *schemas.ServiceSpec {
	if rc == nil {
		return nil
	}
	var lists [][]schemas.ServiceSpec
	if rc.Suite != nil {
		lists = append(lists, rc.Suite.Services)
	}
	if rc.Task != nil {
		lists = append(lists, rc.Task.Services)
	}
	for _, l := range lists {
		for i := range l {
			if l[i].Role == egress.ServiceRole && !l[i].Hidden {
				return &l[i]
			}
		}
	}
	return nil
}

// runEgressMode is the experiment's egress mode, or "" to keep the
// service's own.
func runEgressMode(rc *schemas.RunContext) string {
	if rc == nil || rc.Manifest == nil {
		return ""
	}
	return rc.Manifest.Experiment.Egress
}

// withEgressEnv returns plan with the proxy variables added where the plan
// sets none. A run without an egress service gets plan back unchanged.
func withEgressEnv(rc *schemas.RunContext, plan *schemas.RunPlan) *schemas.RunPlan {
	spec := egressSpec(rc)
	if spec == nil || plan == nil {
		return plan
	}
	noProxy := []string{ToolProxyHost, NeutralToolProxyHost}
	for _, l := range [][]schemas.ServiceSpec{suiteServices(rc), taskServices(rc)} {
		for _, s := range l {
			noProxy = append(noProxy, s.ID)
		}
	}
	noProxy = append(noProxy, egress.HostsOf(plan.Env)...)
	if rc.Task != nil && rc.Task.Environment != nil {
		noProxy = append(noProxy, egress.HostsOf(rc.Task.Environment.Env)...)
	}
	cp := *plan
	cp.Env = make(map[string]string, len(plan.Env)+16)
	for k, v := range egress.ClientEnv(egress.ProxyURL(spec.ID), noProxy) {
		cp.Env[k] = v
	}
	for k, v := range plan.Env {
		cp.Env[k] = v
	}
	return &cp
}

func taskServices(rc *schemas.RunContext) []schemas.ServiceSpec {
	if rc.Task == nil {
		return nil
	}
	return rc.Task.Services
}

func suiteServices(rc *schemas.RunContext) []schemas.ServiceSpec {
	if rc.Suite == nil {
		return nil
	}
	return rc.Suite.Services
}

// egressServiceEnv sets the experiment's mode on the egress service.
func egressServiceEnv(rc *schemas.RunContext, spec schemas.ServiceSpec, env map[string]string) {
	if spec.Role != egress.ServiceRole {
		return
	}
	if m := runEgressMode(rc); m != "" {
		env[egress.EnvMode] = m
	}
}

func egressNetwork(runID string) string { return "bench-enet-" + runID }

// connectEgressNetwork gives the egress service a route out in
// live-allowlisted mode: a fresh bridge network with the run's labels (so
// Destroy and the janitor remove it) that only the service joins. Every
// other mode leaves it on the run's internal networks.
func (e *dockerWorkspace) connectEgressNetwork(ctx context.Context, rc *schemas.RunContext, expID, runID string, spec schemas.ServiceSpec, container string) error {
	if spec.Role != egress.ServiceRole || runEgressMode(rc) != egress.ModeLive {
		return nil
	}
	n := egressNetwork(runID)
	if _, err := e.cfg.Docker.Run(ctx, "network", "rm", n); err != nil {
		return fmt.Errorf("remove stale network %s: %w", n, err)
	}
	args := append([]string{"network", "create"}, utils.LabelArgs(expID, runID)...)
	if _, err := e.cfg.Docker.MustRun(ctx, append(args, n)...); err != nil {
		return fmt.Errorf("create egress network %s: %w", n, err)
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", n, container); err != nil {
		return fmt.Errorf("connect egress network: %w", err)
	}
	return nil
}
