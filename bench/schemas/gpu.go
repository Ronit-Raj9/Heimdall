package schemas

import (
	"context"
	"time"
)

// X7: GPU benchmarking (docs/gpu-tasks.md). This file holds the GPU types:
// what a task asks for (Task.Resources), what a machine has (GPUInfo), what
// a run was given and recorded (RunContext.GPU, Run.Hardware) and what a
// remote worker offers (WorkerRegister.Capabilities). Every field that
// carries one of them elsewhere is optional and omitted when empty, so a
// task, run, lock or seal written before GPU support keeps its hash.

// GPU passthrough modes (config gpu.mode, GPUAssignment.Mode).
const (
	// GPUModeCDI passes each GPU with `--device nvidia.com/gpu=<uuid>`, the
	// Container Device Interface name the NVIDIA container toolkit
	// generates. It is the default.
	GPUModeCDI = "cdi"
	// GPUModeDeviceRequests passes each GPU with `--gpus device=<uuid>`,
	// Docker's device requests, for hosts that have the NVIDIA runtime hook
	// but no CDI specification.
	GPUModeDeviceRequests = "device_requests"
)

// Resources is what a task needs from the machine beyond the agent's own
// budgets (Task.Resources).
type Resources struct {
	GPU *GPURequest `json:"gpu,omitempty" yaml:"gpu"`
}

// GPURequest asks for whole GPUs. Count is how many (at least 1); MinVRAMMB
// is the memory each must have, in MiB as nvidia-smi reports it (0 means
// any). A run holds its GPUs alone from before the agent starts until its
// verifier is done: no other run uses them meanwhile.
type GPURequest struct {
	Count     int `json:"count" yaml:"count"`
	MinVRAMMB int `json:"min_vram_mb,omitempty" yaml:"min_vram_mb"`
}

// GPUCount is the number of GPUs a task needs: 0 for a task without a GPU
// request, else its count (at least 1).
func (t *Task) GPUCount() int {
	if t == nil || t.Resources == nil || t.Resources.GPU == nil {
		return 0
	}
	return max(t.Resources.GPU.Count, 1)
}

// GPUInfo is one GPU as nvidia-smi on the host reports it. MemoryMB is the
// total memory in MiB; ComputeCap is the compute capability (7.5 for a
// Turing card); CUDAVersion is the highest CUDA version the driver supports.
type GPUInfo struct {
	Index       int    `json:"index"`
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	MemoryMB    int    `json:"memory_mb"`
	Driver      string `json:"driver"`
	CUDAVersion string `json:"cuda_version,omitempty"`
	ComputeCap  string `json:"compute_cap"`
}

// GPUAssignment is the GPUs one run holds, from the scheduler's pool. It
// lives on the run context only; Run.Hardware is its sealed record.
type GPUAssignment struct {
	Mode     string    `json:"mode"`
	GPUs     []GPUInfo `json:"gpus"`
	SampleMS int       `json:"sample_ms,omitempty"` // the GPU sampler's interval; 0 switches the sampler off
	SMI      string    `json:"-"`                   // nvidia-smi on the host; empty means "nvidia-smi" on PATH
}

// HardwareRecord is the hardware a run ran on (Run.Hardware), sealed with
// run.json. Only runs of GPU tasks have one.
type HardwareRecord struct {
	GPUs    []GPUInfo `json:"gpus,omitempty"`
	Mode    string    `json:"mode,omitempty"`    // cdi | device_requests
	Devices []string  `json:"devices,omitempty"` // what docker was given, e.g. nvidia.com/gpu=GPU-...
	// Sampler sums the host-side GPU samples taken while the agent ran
	// (gpu.samples.jsonl in the run folder); nil when the sampler was off.
	Sampler *GPUSamplerSummary `json:"sampler,omitempty"`
}

// GPUSamplerSummary is what the host-side sampler saw over the agent's
// sessions. One run holds its GPUs alone, so every sample is the run's.
// PeakVRAMMB is the highest memory in use on any of the run's GPUs minus
// that GPU's reading just before the agent started (a display or the driver
// holds some memory on a desktop card); MeanUtilPct is the mean of every
// utilization sample, in percent. Error says why the sampler could not run
// or stopped early; the numbers are then unknown, never zero.
type GPUSamplerSummary struct {
	Samples      int     `json:"samples"`
	IntervalMS   int     `json:"interval_ms"`
	AgentSeconds float64 `json:"agent_seconds"` // agent wall time while the GPUs were held, summed over sessions
	GPUSeconds   float64 `json:"gpu_seconds"`   // AgentSeconds times the number of GPUs
	BaselineMB   float64 `json:"baseline_mb"`
	PeakUsedMB   float64 `json:"peak_used_mb"`
	PeakVRAMMB   float64 `json:"peak_vram_mb"`
	MeanUtilPct  float64 `json:"mean_util_pct"`
	Sessions     int     `json:"sessions"`
	Error        string  `json:"error,omitempty"`
}

// GPUSample is one line of gpu.samples.jsonl: one GPU at one instant.
// Phase is baseline for the reading taken just before the agent starts and
// agent while it runs.
type GPUSample struct {
	At        time.Time `json:"at"`
	Session   int       `json:"session,omitempty"`
	Phase     string    `json:"phase"`
	UUID      string    `json:"uuid"`
	UtilPct   float64   `json:"util_pct"`
	MemUsedMB float64   `json:"mem_used_mb"`
	MemTotal  float64   `json:"mem_total_mb"`
	PowerW    *float64  `json:"power_w,omitempty"`
	TempC     *float64  `json:"temp_c,omitempty"`
}

// WorkerCapabilities is what a remote worker's machine offers beyond slots
// (WorkerRegister.Capabilities). A worker that lists no GPUs is never leased
// a slot of a GPU task.
type WorkerCapabilities struct {
	GPUs []GPUInfo `json:"gpus,omitempty"`
}

// GPUAllocator hands whole GPUs to runs (Registry.GPUs). Acquire blocks
// until enough free GPUs that meet the request exist, or ctx ends; release
// gives them back and is safe to call more than once. Fits says, without
// waiting, whether the machine could ever satisfy a request, and why not.
type GPUAllocator interface {
	Devices() []GPUInfo
	Mode() string
	Fits(req *GPURequest) error
	Acquire(ctx context.Context, req *GPURequest) (*GPUAssignment, func(), error)
}
