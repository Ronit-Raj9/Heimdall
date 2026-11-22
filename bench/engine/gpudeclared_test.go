package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Z05: a machine without GPUs (the hosted server) plans GPU tasks from the
// GPUs the organization's workers declare.
func TestCheckGPUFromDeclaredGPUs(t *testing.T) {
	s := gpuTestSuite()
	conds := func(ids ...string) []condPlan { return []condPlan{{id: "clean", tasks: ids, trials: 1}} }
	l4 := []schemas.GPUInfo{{Index: 0, UUID: "sandbox-modal-L4-0", Name: "NVIDIA L4 24 GB (modal)", MemoryMB: 23034},
		{Index: 1, UUID: "sandbox-modal-L4-1", Name: "NVIDIA L4 24 GB (modal)", MemoryMB: 23034}}
	declared := func(devs []schemas.GPUInfo) *Planner {
		p := &Planner{reg: &schemas.Registry{}}
		WithDeclaredGPUs(func() []schemas.GPUInfo { return devs })(p)
		return p
	}
	if err := declared(l4).checkGPU(nil, s, conds("small", "big", "two", "cpu")); err != nil {
		t.Fatalf("declared L4s must plan every task: %v", err)
	}
	if err := declared(nil).checkGPU(nil, s, conds("small")); err == nil || !strings.Contains(err.Error(), "no worker of this organization offers one") {
		t.Fatalf("no declared GPU: %v", err)
	}
	small := []schemas.GPUInfo{{UUID: "x", MemoryMB: 4096}}
	if err := declared(small).checkGPU(nil, s, conds("big")); err == nil || !strings.Contains(err.Error(), "16000 MiB") {
		t.Fatalf("too small: %v", err)
	}
	if err := declared(small).checkGPU(nil, s, conds("two")); err == nil {
		t.Fatal("two GPUs on one declared device")
	}
	if err := declared(nil).checkGPU(nil, s, conds("cpu")); err != nil {
		t.Fatalf("a CPU task never needs a declared GPU: %v", err)
	}
}
