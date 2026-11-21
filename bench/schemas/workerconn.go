package schemas

import "time"

// Bring your own worker (package byoworker, FIX_BRIEF decision 6): the
// workers an organization connected, on a cloud platform it pays for, its
// own Kubernetes cluster or its own VM. Each is a runner of the org under
// its own pool; evaluations choose one by that pool.

// Worker connection states, from the runner's live state, the started box
// and the last platform check, in that order of authority.
const (
	WorkerConnReady    = "ready"    // its runner is registered (idle or running)
	WorkerConnStarting = "starting" // the box is starting or the runner is installing
	WorkerConnWaiting  = "waiting"  // nothing started yet (a VM waits for its install command)
	WorkerConnOffline  = "offline"  // its runner went silent
	WorkerConnStopped  = "stopped"  // the box was stopped
	WorkerConnError    = "error"    // the platform refused the credential or the box failed
)

// WorkerHealth is the last check of a connection's platform.
type WorkerHealth struct {
	OK       bool      `json:"ok"`
	Detail   string    `json:"detail"`
	Capacity string    `json:"capacity,omitempty"`
	Checked  time.Time `json:"checked_at"`
}

// WorkerInstance is the box a platform driver started.
type WorkerInstance struct {
	ID     string `json:"id"`
	State  string `json:"state"` // starting | running | stopped | failed | unknown
	Detail string `json:"detail,omitempty"`
}

// WorkerConnection is one connected worker.
type WorkerConnection struct {
	ID            string            `json:"id"` // wkc_...
	Name          string            `json:"name"`
	Pool          string            `json:"pool"` // evaluations name this
	Platform      string            `json:"platform"`
	PlatformTitle string            `json:"platform_title"`
	Settings      map[string]string `json:"settings"`
	Status        string            `json:"status"` // WorkerConn*
	StatusDetail  string            `json:"status_detail"`
	Health        *WorkerHealth     `json:"health,omitempty"`
	Instance      *WorkerInstance   `json:"instance,omitempty"`
	Runner        *RunnerView       `json:"runner,omitempty"` // the registered runner, when it has registered
	Default       bool              `json:"default"`          // new evaluations run here
	CreatedBy     string            `json:"created_by"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// WorkerConnectionCreate is the body of POST /api/bench/workers/connections.
// Secret is the platform credential: an API token, or for Kubernetes a
// kubeconfig (YAML) or a service account credential (JSON with server,
// certificate_authority and token). A VM takes none.
type WorkerConnectionCreate struct {
	Name     string            `json:"name"`
	Platform string            `json:"platform"`
	Settings map[string]string `json:"settings,omitempty"`
	Secret   string            `json:"secret,omitempty"`
}

// WorkerConnectionCreated answers a create: the connection and, for a VM,
// the one-line install command (it holds the runner token and is shown
// once; POST .../install-command shows it again to an admin).
type WorkerConnectionCreated struct {
	Connection WorkerConnection `json:"connection"`
	Install    string           `json:"install,omitempty"`
}

// WorkerInstallCommand is the answer of POST
// /api/bench/workers/connections/{id}/install-command.
type WorkerInstallCommand struct {
	Install string `json:"install"`
}

// WorkerPlatform is one platform an org can connect.
type WorkerPlatform struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Starts is true when Heimdall starts the worker itself (a platform
	// driver); false for a VM the org installs.
	Starts bool `json:"starts"`
}

// WorkerConnectionsList is the answer of GET /api/bench/workers/connections.
type WorkerConnectionsList struct {
	Connections []WorkerConnection `json:"connections"`
	// DefaultWorker is the pool new evaluations run on when they name none:
	// "" is the shared worker (pool default).
	DefaultWorker string           `json:"default_worker"`
	Platforms     []WorkerPlatform `json:"platforms"`
}

// WorkerDefault is the body of PUT /api/bench/workers/default.
type WorkerDefault struct {
	Worker string `json:"worker"` // a connection's pool, or "" for the shared worker
}
