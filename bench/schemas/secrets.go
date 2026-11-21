package schemas

import "context"

// SecretSource resolves named org secrets for a run (Settings > Secrets,
// keystore kind "secret"). Resolve returns name to value for every name it
// can resolve; names it cannot resolve are left out, so the caller can name
// the missing ones. The map stays in memory: never log or persist it.
type SecretSource interface {
	ResolveSecrets(ctx context.Context, names []string) (map[string]string, error)
}

// SecretsHint is the next step a missing secret error points to.
const SecretsHint = "add it in Settings > Secrets"
