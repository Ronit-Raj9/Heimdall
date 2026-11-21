package main

import (
	"context"
	"os"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/toolproxy"
)

// Org secrets in runs (gap G01): the registry's schemas.SecretSource. It
// resolves the secrets a run names (external api auth_env, agent env
// {{secret.NAME}}) from the org's keystore secrets (Settings > Secrets).
// Only local mode falls back to the process environment: a hosted org
// never reads the server's or a runner's own variables.
type orgSecretSource struct {
	keys        keystore.Store
	orgID       string
	envFallback bool // local mode only
}

func (s orgSecretSource) ResolveSecrets(ctx context.Context, names []string) (map[string]string, error) {
	m, err := toolproxy.OrgSecretEnv(ctx, s.keys, s.orgID, names)
	if err != nil {
		if !s.envFallback {
			return nil, err
		}
		m = map[string]string{} // local mode without a key store: the environment serves
	}
	if s.envFallback {
		for _, n := range names {
			if m[n] != "" {
				continue
			}
			if v, ok := os.LookupEnv(n); ok && v != "" {
				m[n] = v
			}
		}
	}
	return m, nil
}
