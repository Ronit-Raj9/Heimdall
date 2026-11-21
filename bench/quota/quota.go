// Package quota holds per-org run limits and the platform kill switch.
// Open returns the database store (store.go); NewStub returns defaults and
// nil errors (local mode and tests).
package quota

import "context"

// Limits are one org's limits.
type Limits struct {
	MaxConcurrentRuns int     `json:"max_concurrent_runs"`
	MaxRunsPerDay     int     `json:"max_runs_per_day"`
	MaxSpendUSDPerDay float64 `json:"max_spend_usd_per_day"`
	MaxImageBytes     int64   `json:"max_image_bytes"`
	AllowCustomImages bool    `json:"allow_custom_images"` // the server admin approved custom images for this org
	SharedPool        bool    `json:"shared_pool"`         // the org's runs may use the shared runner pool
	Runtime           string  `json:"runtime"`             // runc | runsc (gVisor)
}

// Store reads and sets limits.
type Store interface {
	Get(ctx context.Context, orgID string) (Limits, error)
	Set(ctx context.Context, orgID string, l Limits, actor string) error
	Defaults() Limits
	KillSwitch(ctx context.Context) (bool, error)
	SetKillSwitch(ctx context.Context, on bool, actor string) error
}

// DefaultLimits are the limits of an org with no row.
var DefaultLimits = Limits{MaxConcurrentRuns: 2, MaxRunsPerDay: 50, MaxSpendUSDPerDay: 10,
	MaxImageBytes: 2 << 30, Runtime: "runc"}

// NewStub returns the Phase 0 stub store.
func NewStub() Store { return stub{} }

type stub struct{}

func (stub) Get(ctx context.Context, orgID string) (Limits, error)               { return DefaultLimits, nil }
func (stub) Set(ctx context.Context, orgID string, l Limits, actor string) error { return nil }
func (stub) Defaults() Limits                                                    { return DefaultLimits }
func (stub) KillSwitch(ctx context.Context) (bool, error)                        { return false, nil }
func (stub) SetKillSwitch(ctx context.Context, on bool, actor string) error      { return nil }
