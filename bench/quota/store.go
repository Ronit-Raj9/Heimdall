package quota

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("quota", migrations) }

// Runtimes an org's runs may use on the shared pool.
const (
	RuntimeRunc  = "runc"
	RuntimeRunsc = "runsc" // gVisor
)

// Hard ceilings on what Set accepts, so a typo cannot open the platform.
const (
	MaxConcurrentRunsCeiling = 64
	MaxRunsPerDayCeiling     = 10000
	MaxSpendUSDPerDayCeiling = 10000
	MaxImageBytesCeiling     = 50 << 30
)

// ErrInvalid is a limits value Set refuses.
var ErrInvalid = errors.New("quota: invalid limits")

// killSwitchName is the platform_settings row of the kill switch.
const killSwitchName = "kill_switch"

// Options configures the database store.
type Options struct {
	// Defaults are the limits of an org with no row; zero value: DefaultLimits.
	Defaults *Limits
	// CacheFor is how long KillSwitch caches the database value; runner
	// lease calls read it every few seconds. Default 3s; Set refreshes it.
	CacheFor time.Duration
	Now      func() time.Time
}

// Open returns the database-backed store. d must be migrated (the quota
// migration set is registered by this package).
func Open(d *db.DB, o Options) Store {
	def := DefaultLimits
	if o.Defaults != nil {
		def = *o.Defaults
	}
	if o.CacheFor <= 0 {
		o.CacheFor = 3 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &dbStore{db: d, def: Normalize(def), opts: o}
}

type dbStore struct {
	db   *db.DB
	def  Limits
	opts Options

	mu       sync.Mutex
	killAt   time.Time
	killOn   bool
	killRead bool
}

// Normalize fills an empty runtime with runc.
func Normalize(l Limits) Limits {
	l.Runtime = strings.TrimSpace(l.Runtime)
	if l.Runtime == "" {
		l.Runtime = RuntimeRunc
	}
	return l
}

// Validate checks l against the ceilings and the known runtimes.
func Validate(l Limits) error {
	switch {
	case l.MaxConcurrentRuns < 0 || l.MaxConcurrentRuns > MaxConcurrentRunsCeiling:
		return fmt.Errorf("%w: max concurrent runs must be 0 to %d", ErrInvalid, MaxConcurrentRunsCeiling)
	case l.MaxRunsPerDay < 0 || l.MaxRunsPerDay > MaxRunsPerDayCeiling:
		return fmt.Errorf("%w: max runs per day must be 0 to %d", ErrInvalid, MaxRunsPerDayCeiling)
	case l.MaxSpendUSDPerDay < 0 || l.MaxSpendUSDPerDay > MaxSpendUSDPerDayCeiling || l.MaxSpendUSDPerDay != l.MaxSpendUSDPerDay:
		return fmt.Errorf("%w: max spend per day must be 0 to %d USD", ErrInvalid, MaxSpendUSDPerDayCeiling)
	case l.MaxImageBytes < 0 || l.MaxImageBytes > MaxImageBytesCeiling:
		return fmt.Errorf("%w: max image size must be 0 to %d bytes", ErrInvalid, int64(MaxImageBytesCeiling))
	case l.Runtime != RuntimeRunc && l.Runtime != RuntimeRunsc:
		return fmt.Errorf("%w: runtime must be %q or %q (gVisor)", ErrInvalid, RuntimeRunc, RuntimeRunsc)
	}
	return nil
}

func (s *dbStore) Defaults() Limits { return s.def }

func (s *dbStore) Get(ctx context.Context, orgID string) (Limits, error) {
	var raw string
	err := s.db.QueryRow(ctx, `SELECT limits FROM org_quotas WHERE org_id = ?`, orgID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s.def, nil
	}
	if err != nil {
		return Limits{}, fmt.Errorf("read quota: %w", err)
	}
	l := s.def // fields missing from an older row keep the defaults
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return Limits{}, fmt.Errorf("decode quota of %s: %w", orgID, err)
	}
	return Normalize(l), nil
}

func (s *dbStore) Set(ctx context.Context, orgID string, l Limits, actor string) error {
	if strings.TrimSpace(orgID) == "" {
		return fmt.Errorf("%w: no organization", ErrInvalid)
	}
	l = Normalize(l)
	if err := Validate(l); err != nil {
		return err
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO org_quotas (org_id, limits, updated_by, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE SET limits = excluded.limits, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		orgID, string(raw), actor, s.opts.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("write quota: %w", err)
	}
	return nil
}

func (s *dbStore) KillSwitch(ctx context.Context) (bool, error) {
	now := s.opts.Now()
	s.mu.Lock()
	if s.killRead && now.Sub(s.killAt) < s.opts.CacheFor {
		on := s.killOn
		s.mu.Unlock()
		return on, nil
	}
	s.mu.Unlock()
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM platform_settings WHERE name = ?`, killSwitchName).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read kill switch: %w", err)
	}
	on := v == "1"
	s.mu.Lock()
	s.killOn, s.killAt, s.killRead = on, now, true
	s.mu.Unlock()
	return on, nil
}

func (s *dbStore) SetKillSwitch(ctx context.Context, on bool, actor string) error {
	v := "0"
	if on {
		v = "1"
	}
	now := s.opts.Now()
	_, err := s.db.Exec(ctx, `INSERT INTO platform_settings (name, value, updated_by, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET value = excluded.value, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		killSwitchName, v, actor, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("write kill switch: %w", err)
	}
	s.mu.Lock()
	s.killOn, s.killAt, s.killRead = on, now, true
	s.mu.Unlock()
	return nil
}
