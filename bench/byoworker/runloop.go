package byoworker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Runloop devboxes (https://docs.runloop.ai, REST API at
// https://api.runloop.ai, bearer API key). A worker is one devbox whose
// entrypoint is the runner: the devbox lives as long as the runner does, up
// to keep_alive_time_seconds (48 hours at most). Docker runs inside a
// devbox (Runloop's Docker-in-Docker capability), which the runner needs for
// the agent containers.
//
//	POST /v1/devboxes                create (name, entrypoint, environment_variables,
//	                                 blueprint_name, metadata, launch_parameters)
//	GET  /v1/devboxes/{id}           status
//	POST /v1/devboxes/{id}/shutdown  stop
//	GET  /v1/devboxes?limit=1        credential check

// RunloopBaseURL is Runloop's public API.
const RunloopBaseURL = "https://api.runloop.ai"

// runloopSizes are Runloop's predefined devbox sizes (docs: Configuring
// Devbox Instance Sizes): CPU, memory, disk.
var runloopSizes = map[string]string{
	"X_SMALL":  "0.5 CPU, 1 GB memory, 4 GB disk",
	"SMALL":    "1 CPU, 2 GB memory, 4 GB disk",
	"MEDIUM":   "2 CPU, 4 GB memory, 8 GB disk",
	"LARGE":    "2 CPU, 8 GB memory, 16 GB disk",
	"X_LARGE":  "4 CPU, 16 GB memory, 16 GB disk",
	"XX_LARGE": "8 CPU, 32 GB memory, 16 GB disk",
}

// Runloop settings.
const (
	RunloopSize      = "size"      // resource_size_request, default X_LARGE
	RunloopBlueprint = "blueprint" // blueprint_name, optional: an image with Docker and the runner preinstalled
	RunloopHours     = "hours"     // keep alive, 1 to 48, default 48
)

// Runloop is the Runloop devbox driver.
type Runloop struct {
	// BaseURL is the API address; "" is RunloopBaseURL. Set by tests only:
	// an org never chooses where the server sends its token.
	BaseURL string
	HTTP    *http.Client     // nil: a client with a 30 s timeout
	Now     func() time.Time // nil: time.Now
}

// Platform implements Driver.
func (Runloop) Platform() string { return PlatformRunloop }

// Validate implements Driver.
func (Runloop) Validate(in map[string]string, secret string) (map[string]string, error) {
	s, err := settingsOnly(in, RunloopSize, RunloopBlueprint, RunloopHours)
	if err != nil {
		return nil, err
	}
	if s[RunloopSize] == "" {
		s[RunloopSize] = "X_LARGE"
	}
	if _, ok := runloopSizes[s[RunloopSize]]; !ok {
		return nil, fmt.Errorf("%w: size must be one of X_SMALL, SMALL, MEDIUM, LARGE, X_LARGE, XX_LARGE", ErrInvalid)
	}
	if h := s[RunloopHours]; h != "" {
		n, err := strconv.Atoi(h)
		if err != nil || n < 1 || n > 48 {
			return nil, fmt.Errorf("%w: hours must be 1 to 48", ErrInvalid)
		}
	}
	if b := s[RunloopBlueprint]; b != "" && !validName(b, 128) {
		return nil, fmt.Errorf("%w: blueprint must be a blueprint name (letters, digits, '.', '_', '-')", ErrInvalid)
	}
	if len(secret) < 8 || len(secret) > 512 {
		return nil, fmt.Errorf("%w: paste the Runloop API key (Settings, API keys in the Runloop dashboard)", ErrInvalid)
	}
	return s, nil
}

func (r Runloop) client(t Target) *apiClient {
	base := r.BaseURL
	if base == "" {
		base = RunloopBaseURL
	}
	return newAPIClient(base, t.Secret, r.HTTP)
}

func (r Runloop) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Check implements Driver: lists one devbox.
func (r Runloop) Check(ctx context.Context, t Target) Health {
	var out struct {
		HasMore bool `json:"has_more"`
	}
	err := r.client(t).do(ctx, http.MethodGet, "/v1/devboxes?limit=1&include_total_count=false", nil, &out)
	size := t.Settings[RunloopSize]
	if size == "" {
		size = "X_LARGE"
	}
	return checkResult(r.now(), err, "Runloop accepted the API key.", "Runloop "+size+": "+runloopSizes[size])
}

type runloopDevbox struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
}

func (d runloopDevbox) instance() Instance {
	in := Instance{ID: d.ID, Detail: d.Status}
	switch d.Status {
	case "queued", "scheduled", "provisioning", "initializing", "resuming":
		in.State = InstanceStarting
	case "running":
		in.State = InstanceRunning
	case "suspending", "suspended", "shutdown":
		in.State = InstanceStopped
	case "failure":
		in.State = InstanceFailed
		if d.FailureReason != "" {
			in.Detail = "failure: " + d.FailureReason
		}
	default:
		in.State = InstanceUnknown
	}
	return in
}

// Start implements Driver: one devbox whose entrypoint installs and runs
// the runner.
func (r Runloop) Start(ctx context.Context, t Target, b Boot) (Instance, error) {
	if err := checkBoot(b); err != nil {
		return Instance{}, err
	}
	hours := 48
	if h, err := strconv.Atoi(t.Settings[RunloopHours]); err == nil {
		hours = h
	}
	size := t.Settings[RunloopSize]
	if size == "" {
		size = "X_LARGE"
	}
	body := map[string]any{
		"name":                  b.Name,
		"entrypoint":            BootScript(),
		"environment_variables": BootEnv(b, PlatformRunloop),
		"metadata":              map[string]string{"heimdall_pool": b.Pool, "heimdall_worker": b.Name},
		"launch_parameters": map[string]any{
			"resource_size_request":   size,
			"keep_alive_time_seconds": hours * 3600,
		},
	}
	if bp := t.Settings[RunloopBlueprint]; bp != "" {
		body["blueprint_name"] = bp
	}
	var out runloopDevbox
	if err := r.client(t).do(ctx, http.MethodPost, "/v1/devboxes", body, &out); err != nil {
		return Instance{}, fmt.Errorf("create the Runloop devbox: %w", err)
	}
	if out.ID == "" {
		return Instance{}, fmt.Errorf("create the Runloop devbox: the answer has no devbox id")
	}
	return out.instance(), nil
}

// Status implements Driver.
func (r Runloop) Status(ctx context.Context, t Target, id string) (Instance, error) {
	var out runloopDevbox
	if err := r.client(t).do(ctx, http.MethodGet, "/v1/devboxes/"+url.PathEscape(id), nil, &out); err != nil {
		if isStatus(err, http.StatusNotFound) {
			return Instance{ID: id, State: InstanceStopped, Detail: "the devbox no longer exists"}, nil
		}
		return Instance{}, fmt.Errorf("read the Runloop devbox: %w", err)
	}
	return out.instance(), nil
}

// Stop implements Driver: shuts the devbox down.
func (r Runloop) Stop(ctx context.Context, t Target, id string) error {
	err := r.client(t).do(ctx, http.MethodPost, "/v1/devboxes/"+url.PathEscape(id)+"/shutdown", map[string]any{}, nil)
	if err != nil && !isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("shut the Runloop devbox down: %w", err)
	}
	return nil
}

// validName reports whether s is 1..max letters, digits, '.', '_' or '-'.
func validName(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
