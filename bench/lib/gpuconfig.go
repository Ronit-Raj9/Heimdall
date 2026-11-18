package lib

import "fmt"

// GPUConfig is the gpu section of bench.yaml (X7, docs/gpu-tasks.md).
//
//	gpu:
//	  mode: cdi            # cdi (default) | device_requests | off
//	  devices: [0]         # GPUs the pool holds, by index or UUID; empty or [all] is every GPU
//	  sample_ms: 500       # host-side nvidia-smi interval; 0 means 500, negative switches the sampler off
//	  smi: nvidia-smi      # the host's nvidia-smi; empty finds it on PATH
//
// Mode cdi passes each GPU as `--device nvidia.com/gpu=<uuid>` (the NVIDIA
// container toolkit's CDI specification must exist: `nvidia-ctk cdi
// generate`); device_requests passes `--gpus device=<uuid>` through the
// NVIDIA runtime hook. Neither uses privileged mode. Off keeps GPU tasks off
// even where a GPU exists: the planner then refuses them with the reason.
type GPUConfig struct {
	Mode     string   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Devices  []string `yaml:"devices,omitempty" json:"devices,omitempty"`
	SampleMS int      `yaml:"sample_ms,omitempty" json:"sample_ms,omitempty"`
	SMI      string   `yaml:"smi,omitempty" json:"smi,omitempty"`
}

// GPUModeOff switches GPU tasks off (GPUConfig.Mode).
const GPUModeOff = "off"

// Validate checks the mode and the sampler interval.
func (g GPUConfig) Validate() error {
	switch g.Mode {
	case "", "cdi", "device_requests", GPUModeOff:
	default:
		return fmt.Errorf("gpu.mode must be cdi, device_requests or off, got %q", g.Mode)
	}
	if g.SampleMS > 0 && g.SampleMS < 100 {
		return fmt.Errorf("gpu.sample_ms %d is below 100 ms", g.SampleMS)
	}
	return nil
}
