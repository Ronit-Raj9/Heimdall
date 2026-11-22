package byoworker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RunPod GPU pods (https://docs.runpod.io/api-reference, REST API at
// https://rest.runpod.io/v1, bearer API key), lane Z05. A worker is one pod
// with the GPUs the org picks; its start command installs and runs the
// runner, which detects the pod's GPUs with nvidia-smi and registers them,
// so the coordinator leases it the org's GPU slots (KernelBench style).
//
//	GET    /v1/pods             credential check
//	POST   /v1/pods             create (name, imageName, gpuTypeIds, gpuCount,
//	                            cloudType, containerDiskInGb, env, dockerStartCmd)
//	GET    /v1/pods/{id}        status (desiredStatus)
//	DELETE /v1/pods/{id}        terminate
//
// A RunPod pod is a container, not a VM: it has no Docker daemon unless the
// image brings one that can run there. The runner starts agent containers
// with Docker, so the org picks an image that has Docker working (the
// image setting); the boot line tries to install it otherwise and the
// worker reports unhealthy when it cannot (docs/runners.md).

// RunPodBaseURL is RunPod's public REST API.
const RunPodBaseURL = "https://rest.runpod.io"

// PlatformRunPod is RunPod GPU pods.
const PlatformRunPod = "runpod"

// RunPod settings.
const (
	RunPodGPUType   = "gpu_type"   // a RunPod GPU id, default "NVIDIA L4"
	RunPodGPUCount  = "gpu_count"  // 1 to 8, default 1
	RunPodCloudType = "cloud_type" // SECURE (default) or COMMUNITY
	RunPodImage     = "image"      // default RunPodDefaultImage
	RunPodDiskGB    = "disk_gb"    // container disk, 20 to 500, default 60
)

// RunPodDefaultImage is the pod image when the org names none: CUDA on
// Ubuntu, so nvidia-smi and the CUDA runtime are there for GPU tasks.
const RunPodDefaultImage = "nvidia/cuda:12.4.1-runtime-ubuntu22.04"

// runPodGPUs are RunPod GPU ids with their memory and, where checked, the
// list price per GPU hour, secure then community cloud
// (https://www.runpod.io/pricing, as quoted in audit/INFRA_I1_RUNTIMES.md
// 5.9). Other ids are accepted too.
var runPodGPUs = map[string]string{
	"NVIDIA L4":             "24 GB",
	"NVIDIA L40S":           "48 GB, $1.09 or $0.79 per hour",
	"NVIDIA A100 80GB PCIe": "80 GB, $2.09 or $1.19 per hour",
	"NVIDIA H100 PCIe":      "80 GB, $2.89 or $1.99 per hour",
	"NVIDIA H100 80GB HBM3": "80 GB",
}

// RunPod is the RunPod pod driver.
type RunPod struct {
	// BaseURL is the API address; "" is RunPodBaseURL. Set by tests only.
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// Platform implements Driver.
func (RunPod) Platform() string { return PlatformRunPod }

// Validate implements Driver.
func (RunPod) Validate(in map[string]string, secret string) (map[string]string, error) {
	s, err := settingsOnly(in, RunPodGPUType, RunPodGPUCount, RunPodCloudType, RunPodImage, RunPodDiskGB)
	if err != nil {
		return nil, err
	}
	if s[RunPodGPUType] == "" {
		s[RunPodGPUType] = "NVIDIA L4"
	}
	if g := s[RunPodGPUType]; !strings.HasPrefix(g, "NVIDIA ") && !strings.HasPrefix(g, "AMD ") || len(g) > 64 {
		return nil, fmt.Errorf("%w: gpu_type must be a RunPod GPU id such as \"NVIDIA L4\" or \"NVIDIA H100 PCIe\"", ErrInvalid)
	}
	if c := s[RunPodGPUCount]; c != "" {
		if n, err := strconv.Atoi(c); err != nil || n < 1 || n > 8 {
			return nil, fmt.Errorf("%w: gpu_count must be 1 to 8", ErrInvalid)
		}
	}
	switch strings.ToUpper(s[RunPodCloudType]) {
	case "", "SECURE":
		s[RunPodCloudType] = "SECURE"
	case "COMMUNITY":
		s[RunPodCloudType] = "COMMUNITY"
	default:
		return nil, fmt.Errorf("%w: cloud_type must be SECURE or COMMUNITY", ErrInvalid)
	}
	if d := s[RunPodDiskGB]; d != "" {
		if n, err := strconv.Atoi(d); err != nil || n < 20 || n > 500 {
			return nil, fmt.Errorf("%w: disk_gb must be 20 to 500", ErrInvalid)
		}
	}
	if img := s[RunPodImage]; img != "" && (len(img) > 256 || strings.ContainsAny(img, " \t")) {
		return nil, fmt.Errorf("%w: image must be an image reference", ErrInvalid)
	}
	if len(secret) < 8 || len(secret) > 512 {
		return nil, fmt.Errorf("%w: paste the RunPod API key (Settings, API Keys in the RunPod console)", ErrInvalid)
	}
	return s, nil
}

func (r RunPod) client(t Target) *apiClient {
	base := r.BaseURL
	if base == "" {
		base = RunPodBaseURL
	}
	return newAPIClient(base, t.Secret, r.HTTP)
}

func (r RunPod) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func runPodCount(t Target) int {
	if n, err := strconv.Atoi(t.Settings[RunPodGPUCount]); err == nil && n > 0 {
		return n
	}
	return 1
}

// Check implements Driver: lists the account's pods.
func (r RunPod) Check(ctx context.Context, t Target) Health {
	var out []map[string]any
	err := r.client(t).do(ctx, http.MethodGet, "/v1/pods", nil, &out)
	g := t.Settings[RunPodGPUType]
	if g == "" {
		g = "NVIDIA L4"
	}
	capacity := fmt.Sprintf("RunPod %d x %s", runPodCount(t), g)
	if p := runPodGPUs[g]; p != "" {
		capacity += " (" + p + ")"
	}
	return checkResult(r.now(), err, "RunPod accepted the API key.", capacity)
}

type runPodPod struct {
	ID            string `json:"id"`
	DesiredStatus string `json:"desiredStatus"`
	LastStatus    string `json:"lastStatusChange"`
}

func (p runPodPod) instance() Instance {
	in := Instance{ID: p.ID, Detail: p.DesiredStatus}
	if p.LastStatus != "" {
		in.Detail = p.DesiredStatus + ": " + p.LastStatus
	}
	switch p.DesiredStatus {
	case "CREATED", "PENDING":
		in.State = InstanceStarting
	case "RUNNING":
		in.State = InstanceRunning
	case "EXITED", "TERMINATED":
		in.State = InstanceStopped
	case "FAILED":
		in.State = InstanceFailed
	default:
		in.State = InstanceUnknown
	}
	return in
}

// Start implements Driver: one pod whose start command installs and runs
// the runner.
func (r RunPod) Start(ctx context.Context, t Target, b Boot) (Instance, error) {
	if err := checkBoot(b); err != nil {
		return Instance{}, err
	}
	img := t.Settings[RunPodImage]
	if img == "" {
		img = RunPodDefaultImage
	}
	g := t.Settings[RunPodGPUType]
	if g == "" {
		g = "NVIDIA L4"
	}
	disk := 60
	if n, err := strconv.Atoi(t.Settings[RunPodDiskGB]); err == nil {
		disk = n
	}
	body := map[string]any{
		"name":              b.Name,
		"imageName":         img,
		"computeType":       "GPU",
		"gpuTypeIds":        []string{g},
		"gpuCount":          runPodCount(t),
		"cloudType":         t.Settings[RunPodCloudType],
		"containerDiskInGb": disk,
		"env":               BootEnv(b, PlatformRunPod),
		"dockerStartCmd":    []string{"/bin/sh", "-c", BootScript()},
	}
	var out runPodPod
	if err := r.client(t).do(ctx, http.MethodPost, "/v1/pods", body, &out); err != nil {
		return Instance{}, fmt.Errorf("create the RunPod pod: %w", err)
	}
	if out.ID == "" {
		return Instance{}, fmt.Errorf("create the RunPod pod: the answer has no pod id")
	}
	return out.instance(), nil
}

// Status implements Driver.
func (r RunPod) Status(ctx context.Context, t Target, id string) (Instance, error) {
	var out runPodPod
	if err := r.client(t).do(ctx, http.MethodGet, "/v1/pods/"+url.PathEscape(id), nil, &out); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return Instance{ID: id, State: InstanceStopped, Detail: "the pod no longer exists"}, nil
		}
		return Instance{}, fmt.Errorf("read the RunPod pod: %w", err)
	}
	return out.instance(), nil
}

// Stop implements Driver: terminates the pod (its GPUs stop billing).
func (r RunPod) Stop(ctx context.Context, t Target, id string) error {
	err := r.client(t).do(ctx, http.MethodDelete, "/v1/pods/"+url.PathEscape(id), nil, nil)
	if err != nil && !isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("terminate the RunPod pod: %w", err)
	}
	return nil
}
