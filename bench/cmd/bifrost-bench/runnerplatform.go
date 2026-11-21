package main

// Bring your own worker (lane X19a), runner side: an org whose default
// worker is its own cloud sandbox platform (bench/sandbox) gets a worker
// loop whose registry runs every run in that platform, with the org's token
// from its claim (the assignment list carries only a stamp), and whose
// gateway preflight probes egress from a sandbox of that platform.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/k8s"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

// platformKey identifies a platform assignment without holding its token:
// a loop restarts when the platform, token or endpoint changes. The server
// stamps the assignment (a hash of token and endpoint); an unstamped one,
// as in a claim built by hand, is hashed here.
func platformKey(p *sharedpool.PlatformAssignment) string {
	if p == nil {
		return ""
	}
	stamp := p.Stamp
	if stamp == "" {
		sum := sha256.Sum256([]byte(p.Token + "\x00" + p.Endpoint))
		stamp = hex.EncodeToString(sum[:8])
	}
	return p.Name + ":" + stamp
}

// sandboxRegistry is reg with its environment replaced by the sandbox
// driver of the org's platform and its gateway wrapped with the sandbox
// egress probe. The token is checked first, so a revoked one stops here
// with a reason instead of failing every run.
func sandboxRegistry(ctx context.Context, a *app, reg *schemas.Registry, pa *sharedpool.PlatformAssignment) (*schemas.Registry, error) {
	p, err := sandbox.New(pa.Name, sandbox.Credentials{Token: pa.Token, Endpoint: pa.Endpoint})
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := p.Check(cctx); err != nil {
		return nil, err
	}
	if a.cfg.Gateway.PublicURL == "" {
		return nil, fmt.Errorf("%s", sandbox.RefuseNoPublicGateway)
	}
	d, err := sandbox.NewDriver(sandbox.DriverConfig{
		Platform: p, Local: environments.NewDockerDriver(a.dockerCfg),
		AgentGatewayURL: a.cfg.Gateway.AgentURL, PublicGatewayURL: a.cfg.Gateway.PublicURL,
		HelperImage: a.cfg.Images.Helper, Install: k8s.InstallID(a.cfg.Root), Docker: a.docker, Logger: a.logger,
	})
	if err != nil {
		return nil, err
	}
	out := *reg
	out.Environments = []schemas.Environment{d}
	if reg.Gateway != nil {
		out.Gateway = sandbox.WrapGateway(reg.Gateway, d)
	}
	return &out, nil
}
