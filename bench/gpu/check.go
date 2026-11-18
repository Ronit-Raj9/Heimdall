package gpu

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// CheckReport is what `bifrost-bench gpu check` found: the host's GPUs, the
// ones the config selects, and, per selected GPU, whether a container given
// only that GPU (the way a run gets it) saw it.
type CheckReport struct {
	Mode       string              `json:"mode"`
	Host       []schemas.GPUInfo   `json:"host"`
	Selected   []schemas.GPUInfo   `json:"selected"`
	Containers []ContainerSighting `json:"containers"`
	Image      string              `json:"image"`
	Problems   []string            `json:"problems,omitempty"`
	CheckedAt  time.Time           `json:"checked_at"`
	Requests   []RequestFit        `json:"requests,omitempty"`
	Sampler    *schemas.GPUSample  `json:"sampler,omitempty"` // one host-side sample of the first selected GPU
}

// ContainerSighting is one GPU passed to a container under the run's
// hardening (no privileged mode, every capability dropped, no network,
// unprivileged user, read-only root). Seen is true when nvidia-smi -L inside
// listed exactly that GPU's UUID and no other.
type ContainerSighting struct {
	UUID   string   `json:"uuid"`
	Args   []string `json:"args"`
	Seen   bool     `json:"seen"`
	Listed []string `json:"listed,omitempty"` // UUIDs the container listed
	Error  string   `json:"error,omitempty"`
}

// RequestFit is whether one task's GPU request fits the selected GPUs.
type RequestFit struct {
	Task  string `json:"task"`
	Need  string `json:"need"`
	Fits  bool   `json:"fits"`
	Error string `json:"error,omitempty"`
}

// Check lists the host's GPUs, applies the config's selection, and proves
// for every selected GPU that a container started the way the bench starts
// an agent sees that GPU and only it. image must have glibc (nvidia-smi is
// injected from the host by the container toolkit, and is a glibc binary);
// the bench's helper image does.
func Check(ctx context.Context, docker utils.Docker, smi, mode string, devices []string, image string) *CheckReport {
	if mode == "" {
		mode = schemas.GPUModeCDI
	}
	rep := &CheckReport{Mode: mode, Image: image, CheckedAt: time.Now().UTC()}
	host, err := Detect(ctx, smi, nil)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return rep
	}
	rep.Host = host
	if len(host) == 0 {
		rep.Problems = append(rep.Problems, "nvidia-smi lists no GPU")
		return rep
	}
	sel, err := Select(host, devices)
	if err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return rep
	}
	rep.Selected = sel
	for _, g := range sel {
		rep.Containers = append(rep.Containers, sight(ctx, docker, mode, g, image))
	}
	for _, c := range rep.Containers {
		if !c.Seen {
			rep.Problems = append(rep.Problems, fmt.Sprintf("a container given GPU %s did not see it: %s", c.UUID, firstNonEmpty(c.Error, "nvidia-smi -L listed "+strings.Join(c.Listed, ", "))))
		}
	}
	if len(sel) > 0 {
		a := &schemas.GPUAssignment{Mode: mode, GPUs: sel[:1]}
		raw, err := ExecRunner(ctx, firstNonEmpty(smi, DefaultSMI), "--query-gpu="+sampleQuery, "--format=csv,noheader,nounits", "-i", uuids(a))
		if err != nil {
			rep.Problems = append(rep.Problems, "host-side sample: "+err.Error())
		} else if smp, err := ParseSample(strings.Split(strings.TrimSpace(string(raw)), "\n")[0]); err != nil {
			rep.Problems = append(rep.Problems, "host-side sample: "+err.Error())
		} else {
			smp.At, smp.Phase = time.Now().UTC(), PhaseBaseline
			rep.Sampler = &smp
		}
	}
	return rep
}

// sight runs nvidia-smi -L in a hardened container that holds one GPU.
func sight(ctx context.Context, docker utils.Docker, mode string, g schemas.GPUInfo, image string) ContainerSighting {
	a := &schemas.GPUAssignment{Mode: mode, GPUs: []schemas.GPUInfo{g}}
	dev, err := ContainerArgs(a)
	c := ContainerSighting{UUID: g.UUID, Args: dev}
	if err != nil {
		c.Error = err.Error()
		return c
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	name := "bench-gpucheck-" + strings.TrimPrefix(utils.NewID("g", 6), "g_")
	args := []string{"run", "--rm", "--name", name, "--label", utils.LabelBench + "=1",
		"--network", "none", "--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--read-only", "--pids-limit", "64", "--memory", "256m", "--log-driver", "none"}
	args = append(args, dev...)
	args = append(args, "--entrypoint", "nvidia-smi", image, "-L")
	res, err := docker.Run(cctx, args...)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	if res.ExitCode != 0 {
		c.Error = fmt.Sprintf("exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		return c
	}
	c.Listed = ListedUUIDs(string(res.Stdout))
	c.Seen = len(c.Listed) == 1 && strings.EqualFold(c.Listed[0], g.UUID)
	return c
}

// ListedUUIDs reads the UUIDs of `nvidia-smi -L` output
// ("GPU 0: NVIDIA GeForce GTX 1650 (UUID: GPU-...)"). It is pure.
func ListedUUIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "(UUID: ")
		if i < 0 {
			continue
		}
		rest := line[i+len("(UUID: "):]
		if j := strings.Index(rest, ")"); j > 0 {
			ids = append(ids, strings.TrimSpace(rest[:j]))
		}
	}
	return ids
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
