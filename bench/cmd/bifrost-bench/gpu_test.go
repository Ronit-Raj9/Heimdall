package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: the registry's GPU pool (gpu.go).

func TestGPUAllocatorOffOrMissing(t *testing.T) {
	ctx := context.Background()
	if a := gpuAllocator(ctx, &lib.Config{GPU: lib.GPUConfig{Mode: lib.GPUModeOff}}, schemas.NoopLogger{}); a != nil {
		t.Error("gpu.mode off must give no pool")
	}
	if a := gpuAllocator(ctx, &lib.Config{GPU: lib.GPUConfig{SMI: "/nonexistent/nvidia-smi"}}, schemas.NoopLogger{}); a != nil {
		t.Error("no nvidia-smi must give no pool, and a nil interface, not a typed nil")
	}
}
