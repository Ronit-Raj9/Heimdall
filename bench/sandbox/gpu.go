package sandbox

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Z05: GPU placement on a cloud sandbox platform. Modal sandboxes take a
// GPU (modal.Sandbox.create(gpu="L4:2"), gVisor runtime, the default), so a
// GPU task (KernelBench style, Task.Resources.GPU) runs on the org's Modal
// account instead of a machine with its own NVIDIA card. The runner of
// such an org declares virtual GPUs of one Modal GPU type (DeclaredGPUs):
// the coordinator leases GPU slots by them like by a real GPU worker's
// cards, and the driver asks Modal for that type and count per run.
// Daytona and E2B have no GPU sandboxes and keep refusing GPU tasks.

// GPUType is one GPU a platform rents, with its public price. Prices are
// Modal's list prices per GPU hour (https://modal.com/pricing, read
// 2026-11-22); a run's real cost is what the platform bills the org.
type GPUType struct {
	Name       string  `json:"name"`         // the platform's GPU name (Modal: T4, L4, A10, ...)
	Title      string  `json:"title"`        // plain name for screens
	MemoryMB   int     `json:"memory_mb"`    // per GPU, MiB as nvidia-smi reports it
	ComputeCap string  `json:"compute_cap"`  // CUDA compute capability
	USDPerHour float64 `json:"usd_per_hour"` // per GPU
}

// ModalGPUs are Modal's GPU types, cheapest first.
var ModalGPUs = []GPUType{
	{Name: "T4", Title: "NVIDIA T4 16 GB", MemoryMB: 15360, ComputeCap: "7.5", USDPerHour: 0.59},
	{Name: "L4", Title: "NVIDIA L4 24 GB", MemoryMB: 23034, ComputeCap: "8.9", USDPerHour: 0.80},
	{Name: "A10", Title: "NVIDIA A10 24 GB", MemoryMB: 23028, ComputeCap: "8.6", USDPerHour: 1.10},
	{Name: "L40S", Title: "NVIDIA L40S 48 GB", MemoryMB: 46068, ComputeCap: "8.9", USDPerHour: 1.95},
	{Name: "A100-40GB", Title: "NVIDIA A100 40 GB", MemoryMB: 40960, ComputeCap: "8.0", USDPerHour: 2.10},
	{Name: "A100-80GB", Title: "NVIDIA A100 80 GB", MemoryMB: 81920, ComputeCap: "8.0", USDPerHour: 2.50},
	{Name: "H100", Title: "NVIDIA H100 80 GB", MemoryMB: 81559, ComputeCap: "9.0", USDPerHour: 3.95},
	{Name: "H200", Title: "NVIDIA H200 141 GB", MemoryMB: 143771, ComputeCap: "9.0", USDPerHour: 4.54},
}

// DefaultModalGPU is the GPU type a Modal runner declares when the operator
// names none (HEIMDALL_SANDBOX_GPU): the cheapest card with bf16 and 24 GB.
const DefaultModalGPU = "L4"

// GPUEnv names the GPU type a sandbox runner declares (one Modal GPU name).
const GPUEnv = "HEIMDALL_SANDBOX_GPU"

// MaxGPUsPerSandbox is the most GPUs one Modal sandbox takes.
const MaxGPUsPerSandbox = 8

// LookupModalGPU returns the Modal GPU type named name (case-insensitive;
// A10G is Modal's old name of A10).
func LookupModalGPU(name string) (GPUType, bool) {
	n := strings.ToUpper(strings.TrimSpace(name))
	if n == "A10G" {
		n = "A10"
	}
	for _, g := range ModalGPUs {
		if strings.ToUpper(g.Name) == n {
			return g, true
		}
	}
	return GPUType{}, false
}

// ModalGPUNames lists the Modal GPU names, cheapest first.
func ModalGPUNames() []string {
	out := make([]string, 0, len(ModalGPUs))
	for _, g := range ModalGPUs {
		out = append(out, g.Name)
	}
	return out
}

// GPUCapable is implemented by platforms whose sandboxes can take a GPU.
type GPUCapable interface {
	GPUTypes() []GPUType
}

// GPUTypes implements GPUCapable.
func (m *Modal) GPUTypes() []GPUType { return append([]GPUType(nil), ModalGPUs...) }

// virtualGPUPrefix marks the UUID of a declared GPU: it is a lease token,
// not a card on this machine.
const virtualGPUPrefix = "sandbox-"

// DeclaredGPUs is the GPU pool a runner whose placement is platform
// declares: MaxGPUsPerSandbox virtual devices of type t. Their UUIDs are
// sandbox-<platform>-<type>-<n>; nothing on this machine backs them.
func DeclaredGPUs(platform string, t GPUType) []schemas.GPUInfo {
	out := make([]schemas.GPUInfo, 0, MaxGPUsPerSandbox)
	for i := 0; i < MaxGPUsPerSandbox; i++ {
		out = append(out, schemas.GPUInfo{Index: i, UUID: fmt.Sprintf("%s%s-%s-%d", virtualGPUPrefix, platform, t.Name, i),
			Name: t.Title + " (" + platform + ")", MemoryMB: t.MemoryMB, ComputeCap: t.ComputeCap, Driver: platform})
	}
	return out
}

// IsDeclaredGPU reports whether g is a declared (virtual) GPU.
func IsDeclaredGPU(g schemas.GPUInfo) bool { return strings.HasPrefix(g.UUID, virtualGPUPrefix) }

// declaredType reads the GPU type back from a declared GPU's UUID.
func declaredType(g schemas.GPUInfo) string {
	rest, ok := strings.CutPrefix(g.UUID, virtualGPUPrefix)
	if !ok {
		return ""
	}
	parts := strings.Split(rest, "-")
	if len(parts) < 3 {
		return ""
	}
	if _, err := strconv.Atoi(parts[len(parts)-1]); err != nil {
		return ""
	}
	return strings.Join(parts[1:len(parts)-1], "-")
}

// gpuTypeFor picks the GPU type of a run: the type of the GPUs the run
// holds when they are declared ones, else the driver's configured type,
// else the cheapest type with the memory the task asks for.
func gpuTypeFor(types []GPUType, configured string, a *schemas.GPUAssignment, req *schemas.GPURequest) (GPUType, error) {
	byName := map[string]GPUType{}
	for _, g := range types {
		byName[strings.ToUpper(g.Name)] = g
	}
	minMB := 0
	if req != nil {
		minMB = req.MinVRAMMB
	}
	pick := func(name string) (GPUType, error) {
		g, ok := byName[strings.ToUpper(name)]
		if !ok {
			return GPUType{}, fmt.Errorf("GPU type %s is not offered (offered: %s)", name, strings.Join(typeNames(types), ", "))
		}
		if g.MemoryMB < minMB {
			return GPUType{}, fmt.Errorf("the task asks for %d MiB per GPU and %s has %d MiB", minMB, g.Name, g.MemoryMB)
		}
		return g, nil
	}
	if a != nil {
		for _, g := range a.GPUs {
			if t := declaredType(g); t != "" {
				return pick(t)
			}
		}
	}
	if configured != "" {
		return pick(configured)
	}
	sorted := append([]GPUType(nil), types...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].USDPerHour < sorted[j].USDPerHour })
	for _, g := range sorted {
		if g.MemoryMB >= minMB {
			return g, nil
		}
	}
	return GPUType{}, fmt.Errorf("no offered GPU has the %d MiB per GPU the task asks for", minMB)
}

func typeNames(types []GPUType) []string {
	out := make([]string, 0, len(types))
	for _, g := range types {
		out = append(out, g.Name)
	}
	return out
}

// gpuSpec is the platform's GPU request for a run: "<type>:<count>", or
// "<type>" for one GPU.
func gpuSpec(t GPUType, count int) string {
	if count <= 1 {
		return t.Name
	}
	return fmt.Sprintf("%s:%d", t.Name, count)
}
