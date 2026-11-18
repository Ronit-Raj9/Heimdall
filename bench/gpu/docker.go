package gpu

import (
	"errors"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// cdiKind is the CDI device kind the NVIDIA container toolkit generates.
const cdiKind = "nvidia.com/gpu"

// DeviceNames are the names docker is given for an assignment's GPUs, in the
// order of the assignment: CDI device names, or the UUIDs a device request
// lists. They go into the run's hardware record.
func DeviceNames(a *schemas.GPUAssignment) []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.GPUs))
	for _, g := range a.GPUs {
		if a.Mode == schemas.GPUModeDeviceRequests {
			out = append(out, g.UUID)
		} else {
			out = append(out, cdiKind+"="+g.UUID)
		}
	}
	return out
}

// ContainerArgs are the `docker create` arguments that pass an assignment's
// GPUs, and nothing else, to one container: `--device nvidia.com/gpu=<uuid>`
// per GPU under CDI, or one `--gpus "device=<uuid>,..."` device request.
// Neither needs privileged mode or any capability, and each names GPUs by
// UUID, so a reordered PCI bus never hands a run the wrong card. A nil
// assignment gives none. It is pure.
func ContainerArgs(a *schemas.GPUAssignment) ([]string, error) {
	if a == nil {
		return nil, nil
	}
	if len(a.GPUs) == 0 {
		return nil, errors.New("gpu assignment holds no GPU")
	}
	for _, g := range a.GPUs {
		if !strings.HasPrefix(g.UUID, "GPU-") || strings.ContainsAny(g.UUID, ",\"= \n") {
			return nil, errors.New("gpu assignment holds a malformed uuid " + g.UUID)
		}
	}
	names := DeviceNames(a)
	switch a.Mode {
	case schemas.GPUModeDeviceRequests:
		v := "device=" + strings.Join(names, ",")
		if len(names) > 1 {
			// The docker CLI reads --gpus as CSV: a list needs the quotes.
			v = `"` + v + `"`
		}
		return []string{"--gpus", v}, nil
	case schemas.GPUModeCDI, "":
		out := make([]string, 0, 2*len(names))
		for _, n := range names {
			out = append(out, "--device", n)
		}
		return out, nil
	default:
		return nil, errors.New("gpu mode " + a.Mode + " is not cdi or device_requests")
	}
}

// Hardware is the sealed hardware record of a run that holds a.
func Hardware(a *schemas.GPUAssignment) *schemas.HardwareRecord {
	if a == nil {
		return nil
	}
	return &schemas.HardwareRecord{GPUs: append([]schemas.GPUInfo(nil), a.GPUs...), Mode: a.Mode, Devices: DeviceNames(a)}
}
