package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X7: GPU benchmarking (docs/gpu-tasks.md): `gpu check` and the machine's
// GPU pool that every command's registry carries.

func init() {
	register("gpu", "GPU support: gpu check [-image <img>] [-suite <id>] [-json] lists the GPUs and proves a container sees them", runGPU)
}

// gpuAllocator is the machine's GPU pool for the registry: every GPU
// nvidia-smi lists that gpu.devices selects, passed the gpu.mode way. It is
// nil, and GPU tasks are refused at plan time with the reason, when
// gpu.mode is off, there is no NVIDIA driver, or detection fails (logged).
func gpuAllocator(ctx context.Context, cfg *lib.Config, logger schemas.Logger) schemas.GPUAllocator {
	if cfg.GPU.Mode == lib.GPUModeOff {
		return nil
	}
	found, err := gpu.Detect(ctx, cfg.GPU.SMI, nil)
	switch {
	case errors.Is(err, gpu.ErrNoSMI):
		logger.Debug("gpu: no nvidia-smi on this host; GPU tasks are refused at plan time")
		return nil
	case err != nil:
		logger.Warn("gpu: detection failed; GPU tasks are refused at plan time", "error", err.Error())
		return nil
	}
	sel, err := gpu.Select(found, cfg.GPU.Devices)
	if err != nil {
		logger.Warn("gpu: gpu.devices does not match this host; GPU tasks are refused at plan time", "error", err.Error())
		return nil
	}
	if len(sel) == 0 {
		return nil
	}
	pool, err := gpu.NewPool(cfg.GPU.Mode, sel, cfg.GPU.SampleMS, cfg.GPU.SMI)
	if err != nil {
		logger.Warn("gpu: pool not built; GPU tasks are refused at plan time", "error", err.Error())
		return nil
	}
	return pool
}

func runGPU(args []string) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench gpu check [-config deploy/bench.yaml] [-image <glibc image>] [-suite <id>] [-json]")
		return exitUsage
	}
	fs := flag.NewFlagSet("gpu check", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	image := fs.String("image", "", "image the container check runs nvidia-smi -L in (needs glibc); default images.helper")
	suiteRef := fs.String("suite", "", "also say whether each GPU task of this suite fits the selected GPUs")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	if a.cfg.GPU.Mode == lib.GPUModeOff {
		fmt.Println("gpu.mode is off: GPU tasks are refused at plan time on this machine")
		return exitOK
	}
	img := *image
	if img == "" {
		img = a.cfg.Images.Helper
	}
	if img == "" {
		return fail("no image for the container check: set images.helper or pass -image")
	}
	rep := gpu.Check(ctx, a.docker, a.cfg.GPU.SMI, a.cfg.GPU.Mode, a.cfg.GPU.Devices, img)
	if *suiteRef != "" {
		s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), *suiteRef)
		if err != nil {
			return fail("load suite %s: %v", *suiteRef, err)
		}
		for _, t := range s.Tasks {
			if t.GPUCount() == 0 {
				continue
			}
			fit := gpu.RequestFit{Task: t.ID, Need: fmt.Sprintf("%d GPU(s), %d MiB each", t.GPUCount(), t.Resources.GPU.MinVRAMMB), Fits: true}
			if err := gpu.Fits(rep.Selected, t.Resources.GPU); err != nil {
				fit.Fits, fit.Error = false, err.Error()
				rep.Problems = append(rep.Problems, "task "+t.ID+" "+err.Error())
			}
			rep.Requests = append(rep.Requests, fit)
		}
	}
	if *asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail("%v", err)
		}
		fmt.Println(string(b))
	} else {
		printGPUCheck(rep)
	}
	if len(rep.Problems) > 0 {
		return exitInfra
	}
	return exitOK
}

func printGPUCheck(rep *gpu.CheckReport) {
	fmt.Printf("mode %s, container check image %s\n", rep.Mode, rep.Image)
	selected := map[string]bool{}
	for _, g := range rep.Selected {
		selected[g.UUID] = true
	}
	fmt.Printf("%-5s %-28s %-10s %-10s %-6s %-8s %-9s %s\n", "index", "name", "memory", "driver", "cuda", "compute", "selected", "uuid")
	for _, g := range rep.Host {
		fmt.Printf("%-5d %-28s %-10s %-10s %-6s %-8s %-9t %s\n", g.Index, g.Name, fmt.Sprintf("%d MiB", g.MemoryMB), g.Driver, g.CUDAVersion, g.ComputeCap, selected[g.UUID], g.UUID)
	}
	for _, c := range rep.Containers {
		state := "seen"
		if !c.Seen {
			state = "NOT SEEN"
		}
		fmt.Printf("container %s %s (%s)\n", strings.Join(c.Args, " "), state, firstNonEmptyString(c.Error, "nvidia-smi -L listed "+strings.Join(c.Listed, ", ")))
	}
	if s := rep.Sampler; s != nil {
		fmt.Printf("host sampler %s: utilization %.0f%%, memory %.0f of %.0f MiB in use\n", s.UUID, s.UtilPct, s.MemUsedMB, s.MemTotal)
	}
	for _, r := range rep.Requests {
		state := "fits"
		if !r.Fits {
			state = "DOES NOT FIT: " + r.Error
		}
		fmt.Printf("task %s needs %s: %s\n", r.Task, r.Need, state)
	}
	for _, p := range rep.Problems {
		fmt.Printf("problem: %s\n", p)
	}
	if len(rep.Problems) == 0 {
		fmt.Println("ok: every selected GPU is visible to a hardened container, alone")
	}
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
