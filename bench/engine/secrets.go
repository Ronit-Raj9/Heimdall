package engine

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Org secrets in runs (gap G01): a run's live and record external APIs read
// their auth_env, and its agent env may reference {{secret.NAME}}. Both
// resolve from the org's secrets (Settings > Secrets) through the
// registry's SecretSource, or, on a remote worker, from the secrets the
// lease delivered (SlotInput.Secrets). Values live in RunContext.Secrets
// only: never logged, written to the run folder or sealed (plan.json is
// scrubbed through utils.RunSecrets).

// missingSecretsError names the secrets a run needs but the org lacks.
func missingSecretsError(missing []string) error {
	if len(missing) == 1 {
		return fmt.Errorf("org secret %s is not set: %s", missing[0], schemas.SecretsHint)
	}
	return fmt.Errorf("org secrets %s are not set: %s", strings.Join(missing, ", "), schemas.SecretsHint)
}

// resolveSecrets fills rc.Secrets with the secrets the run names. A run
// that names none, or a pipeline without a secret source (no lease secrets,
// no registry source), is left as it was: it reads the bench's own
// environment.
func (p *Pipeline) resolveSecrets(ctx context.Context, st *runState) error {
	names := utils.RunSecretNames(st.rc)
	if len(names) == 0 {
		return nil
	}
	var got map[string]string
	switch {
	case st.in.Secrets != nil:
		got = map[string]string{}
		for _, n := range names {
			if v := st.in.Secrets[n]; v != "" {
				got[n] = v
			}
		}
	case p.reg.Secrets != nil:
		m, err := p.reg.Secrets.ResolveSecrets(ctx, names)
		if err != nil {
			return &partError{part: "secrets", err: fmt.Errorf("resolve org secrets: %w", err)}
		}
		got = map[string]string{}
		for _, n := range names {
			if v := m[n]; v != "" {
				got[n] = v
			}
		}
	default:
		return nil
	}
	if missing := utils.MissingSecrets(names, got); len(missing) > 0 {
		return &partError{part: "secrets", err: missingSecretsError(missing)}
	}
	st.rc.Secrets = got
	return nil
}

// expandPlanSecrets replaces {{secret.NAME}} in the plan's env values. A
// run without a secret source (rc.Secrets nil) reads NAME from the bench's
// own environment, as an external API's auth_env does.
func expandPlanSecrets(rc *schemas.RunContext, plan *schemas.RunPlan) error {
	if plan == nil || len(plan.Env) == 0 {
		return nil
	}
	src := rc.Secrets
	if src == nil {
		src = map[string]string{}
		for _, n := range utils.SecretRefs(plan.Env) {
			if v, ok := os.LookupEnv(n); ok {
				src[n] = v
			}
		}
	}
	missing := map[string]bool{}
	for k, v := range plan.Env {
		out, miss := utils.ExpandSecretRefs(v, src)
		for _, n := range miss {
			missing[n] = true
		}
		plan.Env[k] = out
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return missingSecretsError(names)
	}
	return nil
}

// WithSecretCheck makes the planner refuse a plan whose runs name an org
// secret src cannot resolve, with an error naming each missing secret and
// pointing to Settings > Secrets. A remote worker's replan never checks:
// its secrets come with the lease.
func WithSecretCheck(src schemas.SecretSource) PlannerOption {
	return func(p *Planner) { p.secrets = src }
}

// checkSecrets is the plan-time half of resolveSecrets: every slot's
// secret names, resolved once.
func (p *Planner) checkSecrets(ctx context.Context, m *schemas.Manifest, suite *schemas.Suite) error {
	if p.secrets == nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for i := range m.Slots {
		for _, n := range utils.SlotSecretNames(m, suite, &m.Slots[i]) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	got, err := p.secrets.ResolveSecrets(ctx, names)
	if err != nil {
		return fmt.Errorf("resolve org secrets: %w", err)
	}
	if missing := utils.MissingSecrets(names, got); len(missing) > 0 {
		return missingSecretsError(missing)
	}
	return nil
}
