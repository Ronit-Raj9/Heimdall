// Package quota holds per-org run limits and the platform kill switch.
// Open returns the database store (store.go); NewStub returns defaults and
// nil errors (local mode and tests).
package quota

import "context"

// Limits are one org's limits.
type Limits struct {
	MaxConcurrentRuns int     `json:"max_concurrent_runs"`
	MaxRunsPerDay     int     `json:"max_runs_per_day"`      // 0: no cap; enforced at launch and at every lease
	MaxSpendUSDPerDay float64 `json:"max_spend_usd_per_day"` // 0: no cap; enforced at launch and at every lease
	MaxImageBytes     int64   `json:"max_image_bytes"`
	AllowCustomImages bool    `json:"allow_custom_images"` // the server admin approved custom images for this org
	SharedPool        bool    `json:"shared_pool"`         // the org's runs may use the shared runner pool
	Runtime           string  `json:"runtime"`             // runsc (gVisor, the default) | runc (only on a shared worker the operator allowed runc)
}

// Store reads and sets limits.
type Store interface {
	Get(ctx context.Context, orgID string) (Limits, error)
	Set(ctx context.Context, orgID string, l Limits, actor string) error
	Defaults() Limits
	KillSwitch(ctx context.Context) (bool, error)
	SetKillSwitch(ctx context.Context, on bool, actor string) error
}

// DefaultLimits are the limits of an org with no row. The daily run and
// spend caps are off (0) by default: they are enforced when a server admin
// sets them (X01: the shared worker is uncapped for now). Runs are paid for
// with the organization's own model keys. The runtime is gVisor (G3 B8):
// no org gets runc unless a server admin sets it, and a shared worker
// serves runc orgs only under the operator's HEIMDALL_SHARED_ALLOW_RUNC.
var DefaultLimits = Limits{MaxConcurrentRuns: 2, MaxRunsPerDay: 0, MaxSpendUSDPerDay: 0,
	MaxImageBytes: 2 << 30, Runtime: "runsc"}

// NewStub returns the Phase 0 stub store.
func NewStub() Store { return stub{} }

type stub struct{}

func (stub) Get(ctx context.Context, orgID string) (Limits, error)               { return DefaultLimits, nil }
func (stub) Set(ctx context.Context, orgID string, l Limits, actor string) error { return nil }
func (stub) Defaults() Limits                                                    { return DefaultLimits }
func (stub) KillSwitch(ctx context.Context) (bool, error)                        { return false, nil }
func (stub) SetKillSwitch(ctx context.Context, on bool, actor string) error      { return nil }
