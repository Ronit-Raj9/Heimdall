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
	"os"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/gpu"
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

// platformsKey identifies every platform of an assignment (N208): the
// default platform's key and each per-evaluation platform's key, sorted
// and joined by ",". A loop restarts when any of them is added, removed or
// rotated. With no per-evaluation platform it equals platformKey.
func platformsKey(a sharedpool.Assignment) string {
	var keys []string
	if k := platformKey(a.Platform); k != "" {
		keys = append(keys, k)
	}
	var per []string
	for _, p := range a.Platforms {
		if k := platformKey(p); k != "" {
			per = append(per, "+"+k)
		}
	}
	sort.Strings(per)
	return strings.Join(append(keys, per...), ",")
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
		GPUType:     strings.TrimSpace(os.Getenv(sandbox.GPUEnv)),
		RemoteImage: platformRemoteImage(pa, p, a.docker), // fix wave 3 (N216): images go through the org's registry
	})
	if err != nil {
		return nil, err
	}
	out := *reg
	out.Environments = []schemas.Environment{d}
	pool, err := sandboxGPUPool(p, os.Getenv(sandbox.GPUEnv))
	if err != nil {
		return nil, err
	}
	if pool != nil { // Z05: the runner registers the platform's GPUs, so the coordinator leases it GPU slots
		out.GPUs = pool
	}
	if reg.Gateway != nil {
		out.Gateway = sandbox.WrapGateway(reg.Gateway, d)
	}
	return &out, nil
}

// sandboxGPUPool is the GPU pool a runner on a GPU-capable platform
// declares (Z05, sandbox/gpu.go): virtual devices of one GPU type, the
// operator's HEIMDALL_SANDBOX_GPU or sandbox.DefaultModalGPU. The sampler
// is off: no nvidia-smi on this machine sees the platform's cards. Nil for
// a platform without GPU sandboxes.
func sandboxGPUPool(p sandbox.Platform, want string) (schemas.GPUAllocator, error) {
	gc, ok := p.(sandbox.GPUCapable)
	if !ok {
		return nil, nil
	}
	want = strings.TrimSpace(want)
	if want == "" {
		want = sandbox.DefaultModalGPU
	}
	var t *sandbox.GPUType
	for _, g := range gc.GPUTypes() {
		if strings.EqualFold(g.Name, want) {
			g := g
			t = &g
		}
	}
	if t == nil {
		return nil, fmt.Errorf("%s=%s is not a GPU %s offers (one of %s)", sandbox.GPUEnv, want, p.Name(), strings.Join(sandbox.ModalGPUNames(), ", "))
	}
	return gpu.NewPool(schemas.GPUModeCDI, sandbox.DeclaredGPUs(p.Name(), *t), -1, "")
}
