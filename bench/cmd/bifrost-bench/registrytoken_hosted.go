package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
)

// Library registry pull tokens (Z13, INFRA_I4 item 4): the token endpoint
// the library registry sends docker clients to. Configured by environment
// (deploy/hosted/.env.example, docs/hosting.md "Library registry"):
//
//	HEIMDALL_LIBRARY_REGISTRY_TOKEN_DIR  folder with token.key and token.crt (bifrost-bench images token-key)
//	HEIMDALL_LIBRARY_REGISTRY_SERVICE    the registry's auth.token.service (default heimdall-library)
//	HEIMDALL_LIBRARY_REGISTRY_ISSUER     its auth.token.issuer (default heimdall)
//	HEIMDALL_LIBRARY_REGISTRY_ACCESS     access file: namespace and restricted suites per org
//	HEIMDALL_LIBRARY_REGISTRY_SYNC_AUTH  htpasswd line file of the sync identity (bifrost-bench registry htpasswd)
//
// Without the token folder the endpoint answers 503 saying so.
const (
	libraryRegistryTokenDirEnv = "HEIMDALL_LIBRARY_REGISTRY_TOKEN_DIR"
	libraryRegistryServiceEnv  = "HEIMDALL_LIBRARY_REGISTRY_SERVICE"
	libraryRegistryIssuerEnv   = "HEIMDALL_LIBRARY_REGISTRY_ISSUER"
	libraryRegistryAccessEnv   = "HEIMDALL_LIBRARY_REGISTRY_ACCESS"
	libraryRegistrySyncEnv     = "HEIMDALL_LIBRARY_REGISTRY_SYNC_AUTH"
)

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		h := libraryRegistryHandler(env.base.cfg.Server.Mode, env.auth, env.base.logger.Warn)
		if env.auth != nil {
			h.ClientIP = env.auth.ClientIP
		}
		return h, nil
	})
}

// libraryRegistryHandler builds the token handler from the environment. A
// misconfiguration turns the endpoint off with the reason, never the server.
func libraryRegistryHandler(mode schemas.Mode, svc *auth.Service, warn func(string, ...any)) *handlers.RegistryTokenHandler {
	dir := strings.TrimSpace(os.Getenv(libraryRegistryTokenDirEnv))
	if dir == "" {
		return handlers.NewRegistryTokenHandler(mode, nil, nil, nil, "", libraryRegistryTokenDirEnv+" is not set (bifrost-bench images token-key writes the key)")
	}
	issuer, err := imagepin.LoadIssuer(dir, os.Getenv(libraryRegistryIssuerEnv), os.Getenv(libraryRegistryServiceEnv))
	if err != nil {
		warn("library registry tokens are off", "error", err.Error())
		return handlers.NewRegistryTokenHandler(mode, nil, nil, nil, "", err.Error())
	}
	policy, err := imagepin.LoadAccessPolicy(strings.TrimSpace(os.Getenv(libraryRegistryAccessEnv)))
	if err != nil {
		warn("library registry tokens are off", "error", err.Error())
		return handlers.NewRegistryTokenHandler(mode, nil, nil, nil, "", err.Error())
	}
	syncLine := ""
	if p := strings.TrimSpace(os.Getenv(libraryRegistrySyncEnv)); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			warn("library registry: no sync identity, nobody can push", "error", err.Error())
		} else {
			syncLine = strings.TrimSpace(string(raw))
		}
	}
	var resolve handlers.RegistryOrgResolver
	if svc != nil {
		resolve = func(ctx context.Context, secret string) (string, error) {
			p, err := svc.SecretPrincipal(ctx, secret)
			if err != nil {
				return "", err
			}
			if p == nil || p.OrgID == "" || (p.Kind != auth.KindToken && p.Kind != auth.KindRunner) {
				return "", errors.New("not an org credential")
			}
			return p.OrgID, nil
		}
	}
	return handlers.NewRegistryTokenHandler(mode, issuer, policy, resolve, syncLine, "")
}
