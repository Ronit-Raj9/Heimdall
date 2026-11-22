package main

// Z05, hosted wiring: the hosted server has no GPU, so it plans an org's
// GPU tasks from the GPUs that org's workers declare: the remote runners
// registered with its coordinator (their WorkerCapabilities) and, when the
// org's default worker is Modal, the Modal GPUs its shared runner declares
// (sandbox.DeclaredGPUs, the type in HEIMDALL_SANDBOX_GPU). The coordinator
// then leases each GPU slot only to a worker whose GPUs fit it.

import (
	"context"
	"os"
	"time"

	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
)

func hostedDeclaredGPUs(env *hostedEnv, oa *app) func() []schemas.GPUInfo {
	conns := platformConnections(env)
	return func() []schemas.GPUInfo {
		var out []schemas.GPUInfo
		if c, err := hostedOrgCoordinator(env, oa); err == nil && c != nil {
			for _, w := range c.Workers() {
				if w.State != schemas.WorkerLost {
					out = append(out, w.GPUs...)
				}
			}
		}
		if conns != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if l, err := conns.List(ctx, oa.orgID); err == nil && l.Default == sandbox.PlatformModal {
				want := os.Getenv(sandbox.GPUEnv)
				if want == "" {
					want = sandbox.DefaultModalGPU
				}
				if t, ok := sandbox.LookupModalGPU(want); ok {
					out = append(out, sandbox.DeclaredGPUs(sandbox.PlatformModal, t)...)
				}
			}
		}
		return out
	}
}
