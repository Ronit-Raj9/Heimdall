package main

// Fix wave 3 lane H06 (N216 fix 2, N208), runner side: images a sandbox
// platform pulls from the org's own registry.

import (
	"context"

	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

// platformRemoteImage is the sandbox driver's RemoteImage for an org's
// platform with an image registry: each local image is pushed to the
// registry with the org's login under the org's Docker policy (d) and
// pulled by digest from there; a Modal client gets the login for its pulls.
// nil without a registry: the driver then refuses an image that has no
// registry digest, with its reason.
func platformRemoteImage(pa *sharedpool.PlatformAssignment, p sandbox.Platform, d sandbox.DockerRunner) func(context.Context, string) (string, error) {
	if pa == nil || pa.Registry == nil || pa.Registry.Registry == "" {
		return nil
	}
	login := sandbox.ImageRegistry{Registry: pa.Registry.Registry, Username: pa.Registry.Username, Token: pa.Registry.Token}
	if m, ok := p.(*sandbox.Modal); ok {
		m.UseRegistry(login)
	}
	return (&sandbox.RegistryImages{Login: login, Docker: d, HostCheck: registryHostCheck}).Remote
}

// registryHostCheck is checked before every push to an org's registry; nil
// is sandbox.PublicRegistryHost. Tests replace it so they need no DNS.
var registryHostCheck func(ctx context.Context, host string) error
