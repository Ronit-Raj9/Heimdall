// Package byoworker is "bring your own worker" (FIX_BRIEF decision 6): an
// organization runs its agents on compute it owns instead of the shared
// worker. Every kind of worker ends up as the same thing, a remote runner
// (`bifrost-bench runner start`) that registers with the hosted server
// through an org runner token and leases that org's slots over HTTPS. What
// differs is how the runner gets started:
//
//   - a platform driver starts it on a cloud sandbox the org pays for
//     (Runloop devboxes, Fly Machines; Modal, Daytona and E2B in lane X19a)
//     through the platform's official HTTP API, with the org's platform
//     token from the keystore;
//   - the Kubernetes driver starts it as a Deployment in a namespace of the
//     org's own cluster, through an uploaded kubeconfig or service account
//     token scoped to that namespace;
//   - "your own VM" has no driver: the org runs the one-line install script
//     (deploy/runner/install.sh) with the connection's runner token.
//
// A connection (table worker_connections) is one such worker: its platform,
// its non-secret settings, its runner pool, the keystore rows that hold the
// platform credential and the runner token, and the platform's id of the box
// it started. Evaluations pick a worker by its pool (EvaluationSpec.Worker,
// or the org's default worker).
package byoworker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Platforms. The values are stored in worker_connections.platform and sent
// over the API; the UI words are in PlatformTitle.
const (
	PlatformRunloop    = "runloop"
	PlatformFly        = "fly"
	PlatformKubernetes = "kubernetes"
	PlatformVM         = "vm"
	// Lane X19a.
	PlatformModal   = "modal"
	PlatformDaytona = "daytona"
	PlatformE2B     = "e2b"
)

// PlatformTitle is the plain name of a platform for messages.
func PlatformTitle(p string) string {
	switch p {
	case PlatformRunloop:
		return "Runloop"
	case PlatformFly:
		return "Fly Machines"
	case PlatformKubernetes:
		return "your Kubernetes cluster"
	case PlatformVM:
		return "your own VM"
	case PlatformModal:
		return "Modal"
	case PlatformDaytona:
		return "Daytona"
	case PlatformE2B:
		return "E2B"
	}
	return p
}

// Instance states, normalized from each platform's own words.
const (
	InstanceStarting = "starting" // asked for, not running yet
	InstanceRunning  = "running"  // the box runs; the runner registers once installed
	InstanceStopped  = "stopped"  // stopped or removed
	InstanceFailed   = "failed"   // the platform reports a failure
	InstanceUnknown  = "unknown"
)

// Errors.
var (
	// ErrInvalid is a bad request: a setting or credential with the wrong
	// shape. Handlers answer 400.
	ErrInvalid = errors.New("invalid worker request")
	// ErrNotFound is an unknown connection (or a platform's unknown box).
	ErrNotFound = errors.New("worker connection not found")
	// ErrRefused is the platform refusing the credential (401/403).
	ErrRefused = errors.New("the platform refused the credential")
)

// Target is where a driver acts: one connection's settings and its
// platform credential, revealed from the keystore for this call only.
type Target struct {
	Settings map[string]string // non-secret, validated by Driver.Validate
	Secret   string            // platform token, or the Kubernetes credential (JSON, see kube.go)
}

// Boot is what a started worker needs to become a runner of the org.
type Boot struct {
	Name        string // runner name, unique in the org; also the box's name where the platform has one
	Pool        string // runner pool the evaluations choose
	Slots       int    // runs at once
	ServerURL   string // hosted server, https
	RunnerToken string // hmr_..., the connection's own runner token
	Image       string // runner image, for the platforms that run an image (Fly, Kubernetes)
	Labels      []string
}

// Health is the answer of Driver.Check: can the platform be reached with
// the credential, and what one box gets.
type Health = schemas.WorkerHealth

// Instance is one box a driver started.
type Instance = schemas.WorkerInstance

// Driver starts and stops runners on one platform. Every method that
// reaches the platform takes the caller's context; drivers keep no state
// between calls, so one value serves every org.
type Driver interface {
	// Platform names the platform (Platform*).
	Platform() string
	// Validate normalizes and checks the non-secret settings and the
	// credential's shape without any network call. It returns the settings
	// to store.
	Validate(settings map[string]string, secret string) (map[string]string, error)
	// Check reaches the platform with the credential.
	Check(ctx context.Context, t Target) Health
	// Start starts one box running the runner.
	Start(ctx context.Context, t Target, b Boot) (Instance, error)
	// Status reports a box Start returned.
	Status(ctx context.Context, t Target, id string) (Instance, error)
	// Stop stops and removes the box. Stopping a box that is gone is not an
	// error.
	Stop(ctx context.Context, t Target, id string) error
}

// Registry maps platforms to their drivers.
type Registry map[string]Driver

// NewRegistry returns a registry of drivers, keyed by Driver.Platform.
func NewRegistry(ds ...Driver) Registry {
	r := Registry{}
	for _, d := range ds {
		r[d.Platform()] = d
	}
	return r
}

// Platforms lists the registered platforms, sorted.
func (r Registry) Platforms() []string {
	out := make([]string, 0, len(r))
	for p := range r {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// settingsOnly keeps the allowed keys of in, trimmed, and refuses others.
func settingsOnly(in map[string]string, allowed ...string) (map[string]string, error) {
	out := map[string]string{}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for k, v := range in {
		if !ok[k] {
			return nil, fmt.Errorf("%w: unknown setting %q (allowed: %s)", ErrInvalid, k, strings.Join(allowed, ", "))
		}
		v = strings.TrimSpace(v)
		if len(v) > 512 || strings.ContainsAny(v, "\r\n\x00") {
			return nil, fmt.Errorf("%w: setting %s must be one line of at most 512 characters", ErrInvalid, k)
		}
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}
