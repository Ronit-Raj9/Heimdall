package evidencestore

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// OrgSource resolves organizations for the background mirror. Hosted mode
// can pass the tenancy registry's stores, so the mirror shares their
// verification caches; RunsRootOrgs reads the folders.
type OrgSource interface {
	Orgs(ctx context.Context) ([]Org, error)
	Org(ctx context.Context, orgID string) (Org, error)
}

// RunsRootOrgs finds organizations under a runs root (DiscoverOrgs). It
// keeps each organization's store between calls, so the store's
// verification cache makes a repeated sweep cost one stat per sealed file
// instead of a full hash.
type RunsRootOrgs struct {
	Root   string
	Logger schemas.Logger

	mu     sync.Mutex
	stores map[string]*store.FSStore
}

// Orgs implements OrgSource.
func (r *RunsRootOrgs) Orgs(context.Context) ([]Org, error) {
	found, err := DiscoverOrgs(r.Root, r.Logger)
	if err != nil {
		return nil, err
	}
	for i := range found {
		found[i] = r.keep(found[i])
	}
	return found, nil
}

// Org implements OrgSource.
func (r *RunsRootOrgs) Org(_ context.Context, orgID string) (Org, error) {
	o, err := FindOrg(r.Root, orgID, r.Logger)
	if err != nil {
		return Org{}, err
	}
	return r.keep(o), nil
}

func (r *RunsRootOrgs) keep(o Org) Org {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stores == nil {
		r.stores = map[string]*store.FSStore{}
	}
	if st, ok := r.stores[o.ID]; ok && st.Root() == o.Store.Root() {
		return Org{ID: o.ID, Store: st}
	}
	r.stores[o.ID] = o.Store
	return o
}

// Service runs the mirror in the background of a server: Notify queues an
// experiment after one of its runs seals (the hook), and a sweep of every
// organization runs shortly after Start and then every interval, so a seal
// the hook missed still reaches the mirror.
type Service struct {
	mirror   *Mirror
	orgs     OrgSource
	interval time.Duration
	// Debounce is how long Notify waits for more seals of the same burst
	// before a pass; FirstSweep is the delay before the first sweep.
	Debounce   time.Duration
	FirstSweep time.Duration
	// PassTimeout bounds one sweep or one notified pass, so a stalled
	// transfer can not hold the loop forever; the next sweep retries.
	PassTimeout time.Duration

	mu      sync.Mutex
	pending map[[2]string]bool // true: a full pass (NotifyFull), false: incremental
	kick    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewService returns a stopped service. An interval of zero means no sweep
// at all, neither the first one nor periodic ones (Notify only).
func NewService(m *Mirror, orgs OrgSource, interval time.Duration) *Service {
	return &Service{mirror: m, orgs: orgs, interval: interval, Debounce: 5 * time.Second, FirstSweep: 30 * time.Second, PassTimeout: 2 * time.Hour,
		pending: map[[2]string]bool{}, kick: make(chan struct{}, 1)}
}

// Start runs the service until ctx ends or Close is called.
func (s *Service) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.loop(ctx)
}

// Close stops the service and waits for a pass in progress to end.
func (s *Service) Close() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
}

// Notify queues one experiment for a mirror pass. It never blocks; a burst
// of seals collapses into one pass.
func (s *Service) Notify(orgID, expID string) {
	if s == nil || validID("org_id", orgID) != nil || validID("exp_id", expID) != nil {
		return
	}
	s.queue(orgID, expID, false)
}

// NotifyFull queues one experiment for a full pass, which verifies every
// seal and the whole chain again: the pass at experiment end. A seal's
// Notify in the same burst does not downgrade it.
func (s *Service) NotifyFull(orgID, expID string) {
	if s == nil || validID("org_id", orgID) != nil || validID("exp_id", expID) != nil {
		return
	}
	s.queue(orgID, expID, true)
}

func (s *Service) queue(orgID, expID string, full bool) {
	k := [2]string{orgID, expID}
	s.mu.Lock()
	s.pending[k] = s.pending[k] || full
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) loop(ctx context.Context) {
	defer close(s.done)
	// With no interval the service is the seal hook only: no sweep at all.
	var tick, firstC <-chan time.Time
	if s.interval > 0 {
		first := time.NewTimer(s.FirstSweep)
		defer first.Stop()
		firstC = first.C
		t := time.NewTicker(s.interval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.Debounce):
			}
			s.drain(ctx)
		case <-firstC:
			s.sweep(ctx)
		case <-tick:
			s.sweep(ctx)
		}
	}
}

func (s *Service) drain(ctx context.Context) {
	s.mu.Lock()
	keys := make([][2]string, 0, len(s.pending))
	full := s.pending
	for k := range s.pending {
		keys = append(keys, k)
	}
	s.pending = map[[2]string]bool{}
	s.mu.Unlock()
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"/"+keys[i][1] < keys[j][0]+"/"+keys[j][1] })
	for _, k := range keys {
		org, err := s.orgs.Org(ctx, k[0])
		if err != nil {
			s.mirror.logger().Warn("evidence mirror: unknown org", "org_id", k[0], "error", err.Error())
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, s.PassTimeout)
		// A seal's pass is incremental (only the runs sealed since the last
		// pass are verified); the pass at experiment end and every sweep
		// verify everything.
		_, err = s.mirror.ExperimentWith(pctx, org, k[1], MirrorOptions{Incremental: !full[k]})
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			s.mirror.logger().Error("evidence mirror: experiment not mirrored; the next sweep retries", "org_id", k[0], "exp_id", k[1], "error", err.Error())
		}
	}
}

func (s *Service) sweep(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, s.PassTimeout)
	defer cancel()
	orgs, err := s.orgs.Orgs(ctx)
	if err != nil {
		s.mirror.logger().Error("evidence mirror: list orgs", "error", err.Error())
		return
	}
	if _, err := s.mirror.Sweep(ctx, orgs); err != nil && !errors.Is(err, context.Canceled) {
		s.mirror.logger().Warn("evidence mirror sweep", "error", err.Error())
	}
}
