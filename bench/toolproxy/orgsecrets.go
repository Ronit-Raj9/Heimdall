package toolproxy

// Org secrets (web parity lane L06): an org stores named secrets in the
// keystore (Kind "secret") from the Settings page. An external API route
// names its credential with auth_env; when the org has a secret of that
// name, the proxy injects the secret's value instead of reading the
// variable from its own environment. The value only ever travels from the
// keystore into the proxy's Getenv (or the env of the proxy container); it
// is never written to routes.json, a log, a ledger row or a command line.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/keystore"
)

// MaxOrgSecretBytes caps one secret value.
const MaxOrgSecretBytes = 16 << 10

// secretName is an environment variable style name: upper case letters,
// digits and underscores, starting with a letter, at most 64 characters.
var secretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reservedSecretPrefixes and reservedSecretNames are names an org secret may
// not take: they belong to the bench itself or to the process environment.
var (
	reservedSecretPrefixes = []string{"HEIMDALL_", "BENCH_", "BIFROST_", "AWS_", "S3_"}
	reservedSecretNames    = map[string]bool{"PATH": true, "HOME": true, "USER": true, "SHELL": true, "PWD": true, "TMPDIR": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true}
)

// CheckOrgSecretName reports why name cannot be an org secret name, or nil.
func CheckOrgSecretName(name string) error {
	if !secretName.MatchString(name) {
		return fmt.Errorf("secret name %q must be upper case letters, digits and underscores, start with a letter and be at most 64 characters (e.g. TAVILY_API_KEY)", name)
	}
	if reservedSecretNames[name] {
		return fmt.Errorf("secret name %q is reserved", name)
	}
	for _, p := range reservedSecretPrefixes {
		if strings.HasPrefix(name, p) {
			return fmt.Errorf("secret names may not start with %s (reserved for the server)", p)
		}
	}
	return nil
}

// RouteSecretNames returns the auth_env names the external routes of rs read,
// sorted and without duplicates. Replay routes never read a credential.
func RouteSecretNames(rs *Routes) []string {
	if rs == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range rs.Routes {
		x := r.External
		if x == nil || x.AuthEnv == "" || x.Mode == "replay" || seen[x.AuthEnv] {
			continue
		}
		seen[x.AuthEnv] = true
		out = append(out, x.AuthEnv)
	}
	sort.Strings(out)
	return out
}

// OrgSecretEnv resolves names from the org's secrets: it returns name to
// value for every name the org has a secret for. Names the org has no
// secret for are left out, so the caller can fall back to its own
// environment. The map must stay in memory: never log or persist it.
func OrgSecretEnv(ctx context.Context, ks keystore.Store, orgID string, names []string) (map[string]string, error) {
	out := map[string]string{}
	if ks == nil || len(names) == 0 {
		return out, nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	keys, err := ks.List(ctx, orgID, keystore.KindSecret)
	if err != nil {
		return nil, fmt.Errorf("list org secrets: %w", err)
	}
	for _, k := range keys {
		if !want[k.Label] {
			continue
		}
		if _, dup := out[k.Label]; dup {
			continue // the oldest secret of a name wins; the API refuses duplicates
		}
		v, err := ks.Reveal(ctx, orgID, k.ID)
		if err != nil {
			return nil, fmt.Errorf("read org secret %s: %w", k.Label, err)
		}
		out[k.Label] = v
	}
	return out, nil
}

// WithOrgSecrets layers org secrets over fallback (os.Getenv when nil): the
// result is meant for Config.Getenv, so an external route's auth_env resolves
// to the org's secret first.
func WithOrgSecrets(secrets map[string]string, fallback func(string) string) func(string) string {
	return func(name string) string {
		if v, ok := secrets[name]; ok && v != "" {
			return v
		}
		if fallback != nil {
			return fallback(name)
		}
		return ""
	}
}

// OrgSecretsGetenv is the injection hook a runner calls before it starts the
// proxy for an org's run: it resolves the routes' auth_env names from the
// org's secrets and returns the Getenv to put in Config.
func OrgSecretsGetenv(ctx context.Context, ks keystore.Store, orgID string, rs *Routes, fallback func(string) string) (func(string) string, error) {
	m, err := OrgSecretEnv(ctx, ks, orgID, RouteSecretNames(rs))
	if err != nil {
		return nil, err
	}
	return WithOrgSecrets(m, fallback), nil
}
