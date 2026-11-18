package graders

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/compose"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Multi-container tasks (X2, docs/multi-container.md): the verifier of a
// compose task gets the workspace service's environment, and joins the run
// networks of the services it may query by name. Those networks are internal
// (no route out); verify checks that before every grade.

// composeVerifier adds a compose task's verifier inputs to in: the task
// environment's variables, and the networks of the services marked for the
// verifier. A task that needs them while the run's services are gone (a
// regrade after cleanup) cannot be graded: the error says so.
func composeVerifier(rc *schemas.RunContext, in *gradeInputs) error {
	if rc.Task == nil {
		return nil
	}
	if env := rc.Task.Environment; env != nil && len(env.Env) > 0 {
		in.env = env.Env
	}
	want := compose.VerifierServices(rc.Task)
	if len(want) == 0 {
		return nil
	}
	var found []string
	for _, h := range rc.Services {
		if h.Spec.Compose == nil || !h.Spec.Compose.Verifier || h.Network == "" {
			continue
		}
		found = append(found, h.Spec.ID)
		if !slices.Contains(in.networks, h.Network) {
			in.networks = append(in.networks, h.Network)
		}
	}
	if len(found) < len(want) {
		return fmt.Errorf("the verifier queries services %s, but this run has only %s up; a grade needs the run's live services (a regrade after the run was cleaned up cannot reach them)",
			strings.Join(want, ", "), orNone(found))
	}
	return nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// verifierNetworkArgs is the verifier's --network: none, or the first
// service network it joins (the rest are connected after create).
func verifierNetworkArgs(in *gradeInputs) []string {
	if len(in.networks) == 0 {
		return []string{"--network", verifierNetwork}
	}
	return []string{"--network", in.networks[0]}
}

// verifierEnvArgs sets the task environment's variables in the verifier,
// before its identity, so HOME stays the verifier's own. It is pure.
func verifierEnvArgs(in *gradeInputs) []string {
	keys := make([]string, 0, len(in.env))
	for k := range in.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var args []string
	for _, k := range keys {
		args = append(args, "-e", k+"="+in.env[k])
	}
	return args
}

// checkInternal refuses a verifier network that has a route out: only a
// run's internal service networks are joined.
func (c *cleanRoom) checkInternal(ctx context.Context, networks []string) error {
	for _, n := range networks {
		res, err := c.docker.MustRun(ctx, "network", "inspect", "--format", "{{.Internal}}", n)
		if err != nil {
			return fmt.Errorf("inspect verifier network %s: %w", n, err)
		}
		if strings.TrimSpace(string(res.Stdout)) != "true" {
			return fmt.Errorf("verifier network %s is not internal; the verifier joins only a run's internal service networks", n)
		}
	}
	return nil
}

// connectExtra joins the created verifier container to the networks after
// the first.
func (c *cleanRoom) connectExtra(ctx context.Context, container string, networks []string) error {
	for _, n := range networks[min(1, len(networks)):] {
		if _, err := c.docker.MustRun(ctx, "network", "connect", n, container); err != nil {
			return fmt.Errorf("connect verifier to %s: %w", n, err)
		}
	}
	return nil
}
