package gpu

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// DefaultSampleMS is the GPU sampler's interval when the config sets none.
const DefaultSampleMS = 500

var _ schemas.GPUAllocator = (*Pool)(nil)

// Pool gives runs whole GPUs: a GPU is held by at most one run at a time,
// from before the agent starts until the run's verifier is done, so every
// sample the host takes of it belongs to that run and no two runs share
// memory or compute. It is safe for concurrent use.
type Pool struct {
	mode     string
	devs     []schemas.GPUInfo
	sampleMS int
	smi      string

	mu      sync.Mutex
	busy    map[string]bool
	changed chan struct{} // closed and replaced whenever a GPU is released
}

// NewPool returns a pool over devs. mode is schemas.GPUModeCDI (the default
// when empty) or schemas.GPUModeDeviceRequests; sampleMS is the sampler's
// interval (negative switches the sampler off, 0 means DefaultSampleMS);
// smi is the host's nvidia-smi (empty means DefaultSMI on PATH).
func NewPool(mode string, devs []schemas.GPUInfo, sampleMS int, smi string) (*Pool, error) {
	switch mode {
	case "":
		mode = schemas.GPUModeCDI
	case schemas.GPUModeCDI, schemas.GPUModeDeviceRequests:
	default:
		return nil, fmt.Errorf("gpu.mode %q is not cdi or device_requests", mode)
	}
	switch {
	case sampleMS == 0:
		sampleMS = DefaultSampleMS
	case sampleMS < 0:
		sampleMS = 0
	case sampleMS < 100:
		return nil, fmt.Errorf("gpu.sample_ms %d is below 100 ms; nvidia-smi cannot sample faster reliably", sampleMS)
	}
	if smi == "" {
		smi = DefaultSMI
	}
	return &Pool{mode: mode, devs: append([]schemas.GPUInfo(nil), devs...), sampleMS: sampleMS, smi: smi,
		busy: map[string]bool{}, changed: make(chan struct{})}, nil
}

// Devices implements schemas.GPUAllocator.
func (p *Pool) Devices() []schemas.GPUInfo { return append([]schemas.GPUInfo(nil), p.devs...) }

// Mode implements schemas.GPUAllocator.
func (p *Pool) Mode() string { return p.mode }

// Fits implements schemas.GPUAllocator: it says why the request can never be
// met on this machine, or nil.
func (p *Pool) Fits(req *schemas.GPURequest) error {
	return Fits(p.devs, req)
}

// Fits says why devs can never meet req, or nil. It is pure.
func Fits(devs []schemas.GPUInfo, req *schemas.GPURequest) error {
	if req == nil {
		return nil
	}
	count := max(req.Count, 1)
	if len(devs) == 0 {
		return fmt.Errorf("needs %s but this machine has no GPU the bench can use (no NVIDIA driver, or gpu.devices selects none; bifrost-bench gpu check shows what is found)", describe(req))
	}
	ok := 0
	var small []string
	for _, g := range devs {
		if g.MemoryMB >= req.MinVRAMMB {
			ok++
		} else {
			small = append(small, fmt.Sprintf("GPU %d %s has %d MiB", g.Index, g.Name, g.MemoryMB))
		}
	}
	if ok >= count {
		return nil
	}
	if len(small) > 0 && len(devs) >= count {
		return fmt.Errorf("needs %s, but %s", describe(req), strings.Join(small, ", "))
	}
	return fmt.Errorf("needs %s, but this machine has %d GPU(s) the bench can use, %d with enough memory", describe(req), len(devs), ok)
}

// describe renders a request for refusal messages.
func describe(req *schemas.GPURequest) string {
	s := fmt.Sprintf("%d GPU(s)", max(req.Count, 1))
	if req.MinVRAMMB > 0 {
		s += fmt.Sprintf(" with at least %d MiB each", req.MinVRAMMB)
	}
	return s
}

// Acquire implements schemas.GPUAllocator. It takes the lowest-index free
// GPUs that meet the request, waiting while they are held, and fails at once
// when the machine can never meet it.
func (p *Pool) Acquire(ctx context.Context, req *schemas.GPURequest) (*schemas.GPUAssignment, func(), error) {
	if req == nil {
		return nil, func() {}, nil
	}
	if err := p.Fits(req); err != nil {
		return nil, func() {}, err
	}
	count := max(req.Count, 1)
	for {
		p.mu.Lock()
		var picked []schemas.GPUInfo
		for _, g := range p.devs {
			if !p.busy[g.UUID] && g.MemoryMB >= req.MinVRAMMB {
				picked = append(picked, g)
				if len(picked) == count {
					break
				}
			}
		}
		if len(picked) == count {
			for _, g := range picked {
				p.busy[g.UUID] = true
			}
			p.mu.Unlock()
			var once sync.Once
			release := func() { once.Do(func() { p.release(picked) }) }
			return &schemas.GPUAssignment{Mode: p.mode, GPUs: picked, SampleMS: p.sampleMS, SMI: p.smi}, release, nil
		}
		wait := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, func() {}, ctx.Err()
		case <-wait:
		}
	}
}

func (p *Pool) release(gpus []schemas.GPUInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, g := range gpus {
		delete(p.busy, g.UUID)
	}
	close(p.changed)
	p.changed = make(chan struct{})
}

// Busy reports how many GPUs are held right now.
func (p *Pool) Busy() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.busy)
}
