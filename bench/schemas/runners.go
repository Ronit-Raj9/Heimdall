package schemas

import "time"

// This file holds the hosted runner admin API (Runners area,
// docs/runners.md): the runners an organization registered and the runner
// tokens (hmr_...) its admins created. The runner protocol itself is the
// worker API (worker.go).

// Runner states (RunnerView.State). A runner silent for longer than the
// stale threshold is lost; it comes back when it calls again.
const (
	RunnerIdle    = WorkerIdle
	RunnerRunning = WorkerRunning
	RunnerLost    = WorkerLost
)

// RunnerView is one registered runner as GET /api/bench/runners lists it.
type RunnerView struct {
	ID           string    `json:"id"` // rnr_...
	OrgID        string    `json:"org_id"`
	Name         string    `json:"name"` // the runner's -name, its worker id
	Pool         string    `json:"pool"`
	Labels       []string  `json:"labels"`
	Version      string    `json:"version"` // bench build the runner runs
	Host         string    `json:"host"`
	State        string    `json:"state"` // idle | running | lost
	Slots        int       `json:"slots"`
	Running      []string  `json:"running"`       // slot ids it holds now (live; empty when the server restarted)
	RunningSlots int       `json:"running_slots"` // len(running), or the last count recorded
	Completed    int       `json:"completed"`     // runs accepted since the server started
	TokenID      string    `json:"token_id"`
	TokenPrefix  string    `json:"token_prefix,omitempty"`
	LastSeen     time.Time `json:"last_seen"`
	RegisteredAt time.Time `json:"registered_at"`
	Live         bool      `json:"live"` // the coordinator holds a registration for it now
}

// RunnersList is the answer of GET /api/bench/runners.
type RunnersList struct {
	Runners []RunnerView `json:"runners"`
}

// RunnerTokenView is one runner token without its secret.
type RunnerTokenView struct {
	ID         string     `json:"id"` // rtk_...
	OrgID      string     `json:"org_id"`
	Pool       string     `json:"pool"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"` // the 8 lookup characters after hmr_, shown to tell tokens apart
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	State      string     `json:"state"` // active | revoked | expired
}

// Runner token states (RunnerTokenView.State).
const (
	RunnerTokenActive  = "active"
	RunnerTokenRevoked = "revoked"
	RunnerTokenExpired = "expired"
)

// RunnerTokensList is the answer of GET /api/bench/runners/tokens.
type RunnerTokensList struct {
	Tokens []RunnerTokenView `json:"tokens"`
}

// RunnerTokenCreate is the body of POST /api/bench/runners/tokens.
type RunnerTokenCreate struct {
	Pool          string `json:"pool,omitempty"` // default DefaultRunnerPool
	Name          string `json:"name,omitempty"` // a label for the token, e.g. the machine it is for
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
}

// RunnerTokenCreated is the answer to a token creation: the only time the
// token is shown. Command is the exact `bifrost-bench runner start` line and
// Install the install script line for a fresh box.
type RunnerTokenCreated struct {
	Token   string          `json:"token"`
	Info    RunnerTokenView `json:"info"`
	Command string          `json:"command"`
	Install string          `json:"install"`
}
