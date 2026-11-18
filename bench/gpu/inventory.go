// Package gpu is the GPU side of the bench (X7, docs/gpu-tasks.md): it finds
// the host's NVIDIA GPUs with nvidia-smi, keeps the pool that gives each run
// whole GPUs of its own, builds the docker arguments that pass them to the
// agent and verifier containers (never privileged mode), samples the run's
// GPUs from the host while the agent works, and proves a container sees
// them (`bifrost-bench gpu check`).
package gpu

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// DefaultSMI is the nvidia-smi binary looked up on PATH when the config
// names none.
const DefaultSMI = "nvidia-smi"

// inventoryQuery is the nvidia-smi query behind Detect.
const inventoryQuery = "index,uuid,name,memory.total,driver_version,compute_cap"

// ErrNoSMI is returned by Detect when nvidia-smi cannot be run: no NVIDIA
// driver on this host, or the binary is not on PATH.
var ErrNoSMI = errors.New("nvidia-smi not found: no NVIDIA driver on this host, or nvidia-smi is not on PATH")

// Runner runs a host command and returns its stdout. Tests replace it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs the command with os/exec.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	var ee *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, ErrNoSMI
	case errors.As(err, &ee):
		return out, fmt.Errorf("%s: exit %d: %s", name, ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
	case err != nil:
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// cudaRe reads the highest CUDA version the driver supports from the
// nvidia-smi banner. Drivers print "CUDA Version: 12.4" or, from the 600
// series on, "CUDA UMD Version: 13.4".
var cudaRe = regexp.MustCompile(`CUDA (?:UMD )?Version:\s*([0-9]+(?:\.[0-9]+)*)`)

// Detect lists the host's GPUs in index order. The CUDA version comes from
// the nvidia-smi banner and is empty when the banner does not carry it.
func Detect(ctx context.Context, smi string, run Runner) ([]schemas.GPUInfo, error) {
	if smi == "" {
		smi = DefaultSMI
	}
	if run == nil {
		run = ExecRunner
	}
	out, err := run(ctx, smi, "--query-gpu="+inventoryQuery, "--format=csv,noheader,nounits")
	if err != nil {
		return nil, err
	}
	gpus, err := ParseInventory(string(out))
	if err != nil {
		return nil, err
	}
	if banner, err := run(ctx, smi); err == nil {
		if m := cudaRe.FindStringSubmatch(string(banner)); m != nil {
			for i := range gpus {
				gpus[i].CUDAVersion = m[1]
			}
		}
	}
	return gpus, nil
}

// ParseInventory reads the CSV of inventoryQuery (no header, no units). It
// is pure.
func ParseInventory(csv string) ([]schemas.GPUInfo, error) {
	var gpus []schemas.GPUInfo
	for n, line := range strings.Split(strings.TrimSpace(csv), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := splitCSV(line)
		if len(f) != 6 {
			return nil, fmt.Errorf("nvidia-smi inventory line %d: %d fields, want 6: %q", n+1, len(f), line)
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("nvidia-smi inventory line %d: index %q: %w", n+1, f[0], err)
		}
		mem, err := strconv.ParseFloat(f[3], 64)
		if err != nil {
			return nil, fmt.Errorf("nvidia-smi inventory line %d: memory.total %q: %w", n+1, f[3], err)
		}
		if !strings.HasPrefix(f[1], "GPU-") {
			return nil, fmt.Errorf("nvidia-smi inventory line %d: uuid %q does not start with GPU-", n+1, f[1])
		}
		gpus = append(gpus, schemas.GPUInfo{Index: idx, UUID: f[1], Name: f[2], MemoryMB: int(mem), Driver: f[4], ComputeCap: f[5]})
	}
	sort.Slice(gpus, func(i, j int) bool { return gpus[i].Index < gpus[j].Index })
	return gpus, nil
}

// splitCSV splits one nvidia-smi CSV line. nvidia-smi never quotes, and GPU
// names hold no commas.
func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// Select keeps the GPUs the config's gpu.devices names: an index ("0"), a
// UUID ("GPU-..."), or "all". An empty list means all. A name that matches
// no GPU is an error, so a typo never silently runs on another card.
func Select(all []schemas.GPUInfo, devices []string) ([]schemas.GPUInfo, error) {
	if len(devices) == 0 {
		return all, nil
	}
	var out []schemas.GPUInfo
	seen := map[string]bool{}
	for _, d := range devices {
		d = strings.TrimSpace(d)
		if d == "all" {
			return all, nil
		}
		found := false
		for _, g := range all {
			if d == strconv.Itoa(g.Index) || strings.EqualFold(d, g.UUID) {
				found = true
				if !seen[g.UUID] {
					seen[g.UUID] = true
					out = append(out, g)
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("gpu.devices names %q, which matches no GPU on this host (bifrost-bench gpu check lists them)", d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// Model is how hardware parity compares GPUs: the name and the memory, so
// two cards of one model with different memory sizes count as different.
func Model(g schemas.GPUInfo) string {
	return fmt.Sprintf("%s %d MiB", g.Name, g.MemoryMB)
}
