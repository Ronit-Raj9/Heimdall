package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Scheduler defaults.
const (
	DefaultConcurrency    = 2
	DefaultStaleAfter     = 2 * time.Minute
	DefaultHeartbeatEvery = 15 * time.Second
	costMetric            = "cost_usd"
)

// Scheduler leases slots and runs them through the pipeline with a fixed
// number of workers.
type Scheduler struct {
	reg      *schemas.Registry
	pipeline *Pipeline
	janitor  *Janitor
	logger   schemas.Logger
}

// RunOptions configures one scheduler run.
type RunOptions struct {
	Resume         bool          // continue an experiment the store already holds
	Owner          string        // lease owner; default a random id
	Concurrency    int           // overrides Experiment.Concurrency when > 0
	StaleAfter     time.Duration // a lease older than this is reclaimed
	HeartbeatEvery time.Duration
	// Events receives progress for the live view. Sends never block: the
	// channel should be buffered, and events that do not fit are counted in
	// Summary.DroppedEvents. The scheduler never closes it.
	Events chan<- Event
}

// Summary is what one scheduler run did.
type Summary struct {
	ExpID         string  `json:"exp_id"`
	Sealed        int     `json:"sealed"`         // runs sealed by this call, retries and recoveries included
	Skipped       int     `json:"skipped"`        // slots that already had a counted sealed run
	Retried       int     `json:"retried"`        // new runs started after aborted_infra
	Recovered     int     `json:"recovered"`      // interrupted runs sealed on resume
	LeaseHeld     int     `json:"lease_held"`     // slots another live owner holds
	Pending       int     `json:"pending"`        // slots not leased because of the spend cap or cancellation
	Errors        int     `json:"errors"`         // slots that hit a store or seal error
	BudgetStopped bool    `json:"budget_stopped"` // the spend cap stopped leasing
	SpentUSD      float64 `json:"spent_usd"`
	DroppedEvents int     `json:"dropped_events"`
}

// schedRun is the shared state of one Run call.
type schedRun struct {
	s       *Scheduler
	m       *schemas.Manifest
	suite   *schemas.Suite
	opts    RunOptions
	mu      sync.Mutex
	sum     Summary
	stopped bool // spend cap reached
}

// NewScheduler returns a scheduler. janitor may be nil (tests, or a host
// without docker); then no sweep runs at start.
func NewScheduler(reg *schemas.Registry, benchRoot string, logger schemas.Logger, janitor *Janitor) *Scheduler {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &Scheduler{reg: reg, pipeline: NewPipeline(reg, benchRoot, logger), janitor: janitor, logger: logger}
}

// Run executes every slot of the manifest in OrderIndex order.
//
// Without Resume it writes the manifest to the store first and refuses an
// experiment that already exists. With Resume it requires the stored manifest
// to carry the same hash. Either way a slot with a counted sealed run is
// skipped, an interrupted run is sealed (infra_error when its agent started,
// aborted_infra otherwise, then a new run), and a run that aborts before its
// agent starts gets at most two retries. Before each lease the spend so far
// (cost_usd of every sealed run) is compared with Budget.TotalUSD; at or past
// the cap no further slot is leased.
func (s *Scheduler) Run(ctx context.Context, m *schemas.Manifest, opts RunOptions) (*Summary, error) {
	r, err := s.start(ctx, m, opts)
	if err != nil {
		return nil, err
	}
	slots := append([]schemas.Slot(nil), m.Slots...)
	sort.SliceStable(slots, func(i, j int) bool { return slots[i].OrderIndex < slots[j].OrderIndex })
	jobs := make(chan *schemas.Slot)
	go func() {
		defer close(jobs)
		for i := range slots {
			jobs <- &slots[i]
		}
	}()
	workers := opts.Concurrency
	if workers <= 0 {
		workers = m.Experiment.Concurrency
	}
	if workers <= 0 {
		workers = DefaultConcurrency
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for slot := range jobs {
				r.process(ctx, slot)
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	out, stopped := r.sum, r.stopped
	r.mu.Unlock()
	if stopped {
		r.s.logger.Warn("spend cap stopped leasing", "experiment", m.ExpID, "cap_usd", m.Experiment.Budget.TotalUSD,
			"spent_usd", out.SpentUSD, "pending", out.Pending)
		r.emit(Event{Kind: EventBudgetStop, Message: fmt.Sprintf("spend cap %.2f USD reached at %.4f USD; %d slots not leased",
			m.Experiment.Budget.TotalUSD, out.SpentUSD, out.Pending)})
	}
	r.emit(Event{Kind: EventDone, Message: fmt.Sprintf("sealed %d, skipped %d, pending %d", out.Sealed, out.Skipped, out.Pending)})
	r.mu.Lock()
	out.DroppedEvents = r.sum.DroppedEvents
	r.mu.Unlock()
	return &out, ctx.Err()
}

// start checks the manifest, registers or matches it in the store, loads the
// suite, sweeps leftovers and sums the spend so far.
func (s *Scheduler) start(ctx context.Context, m *schemas.Manifest, opts RunOptions) (*schedRun, error) {
	if s.reg == nil || s.reg.Store == nil {
		return nil, errors.New("scheduler: no store registered")
	}
	if m == nil {
		return nil, errors.New("scheduler: manifest is nil")
	}
	h, err := ManifestHash(m)
	if err != nil {
		return nil, err
	}
	if h != m.Hash {
		return nil, fmt.Errorf("manifest %s: hash mismatch: recorded %s, computed %s", m.ExpID, m.Hash, h)
	}
	if opts.Resume {
		stored, err := s.reg.Store.LoadManifest(ctx, m.ExpID)
		if err != nil {
			return nil, fmt.Errorf("resume %s: %w", m.ExpID, err)
		}
		if stored.Hash != m.Hash {
			return nil, fmt.Errorf("resume %s: stored manifest hash %s differs from %s", m.ExpID, stored.Hash, m.Hash)
		}
	} else if err := s.reg.Store.CreateExperiment(ctx, m); err != nil {
		return nil, fmt.Errorf("create experiment %s: %w", m.ExpID, err)
	}
	suite, err := LoadSuite(ctx, s.reg, m.Experiment.Suite)
	if err != nil {
		return nil, err
	}
	if suite.LockHash != m.SuiteLockHash {
		return nil, fmt.Errorf("suite %s lock hash %q differs from the manifest's %q", suite.ID, suite.LockHash, m.SuiteLockHash)
	}
	if opts.Owner == "" {
		opts.Owner = utils.NewID("w", 8)
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = DefaultStaleAfter
	}
	if opts.HeartbeatEvery <= 0 {
		opts.HeartbeatEvery = DefaultHeartbeatEvery
	}
	if s.janitor != nil {
		if _, err := s.janitor.Sweep(ctx, map[string]bool{}); err != nil {
			s.logger.Warn("janitor sweep failed", "experiment", m.ExpID, "error", err.Error())
		}
	}
	r := &schedRun{s: s, m: m, suite: suite, opts: opts, sum: Summary{ExpID: m.ExpID}}
	spent, err := s.spentSoFar(ctx, m)
	if err != nil {
		return nil, err
	}
	r.sum.SpentUSD = spent
	return r, nil
}

// spentSoFar sums cost_usd over the sealed runs of every slot.
func (s *Scheduler) spentSoFar(ctx context.Context, m *schemas.Manifest) (float64, error) {
	var total float64
	for _, slot := range m.Slots {
		recs, err := slotRuns(ctx, s.reg.Store, m.ExpID, slot.SlotID)
		if err != nil {
			return 0, err
		}
		for _, rec := range recs {
			if !rec.Sealed {
				continue
			}
			c, err := readCost(rec.Dir)
			if err != nil {
				s.logger.Warn("unreadable metrics.json", "run", rec.Run.RunID, "error", err.Error())
			}
			total += c
		}
	}
	return total, nil
}

// process runs one slot to a counted run, or decides it needs none.
//
// A slot that already has a counted sealed run is skipped before the budget
// check, so it is never reported as pending.
func (r *schedRun) process(ctx context.Context, slot *schemas.Slot) {
	if ctx.Err() != nil {
		r.count(func(s *Summary) { s.Pending++ })
		return
	}
	recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, slot.SlotID)
	if err != nil {
		r.slotError(slot, err)
		return
	}
	if r.skipDone(slot, recs) {
		return
	}
	if r.overBudget() {
		r.count(func(s *Summary) { s.Pending++ })
		return
	}
	store := r.s.reg.Store
	ok, err := store.Lease(ctx, slot.SlotID, r.opts.Owner, r.opts.StaleAfter)
	if err != nil {
		r.slotError(slot, fmt.Errorf("lease: %w", err))
		return
	}
	if !ok {
		r.count(func(s *Summary) { s.LeaseHeld++ })
		r.emit(Event{Kind: EventLeaseHeld, SlotID: slot.SlotID})
		return
	}
	stop := r.heartbeat(ctx, slot.SlotID)
	defer func() {
		stop()
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := store.Release(rctx, slot.SlotID, r.opts.Owner); err != nil {
			r.s.logger.Warn("release lease", "slot", slot.SlotID, "error", err.Error())
		}
	}()
	attempts, done := r.settle(ctx, slot)
	if done {
		return
	}
	for attempts < maxAttempts && ctx.Err() == nil {
		if attempts > 0 {
			r.count(func(s *Summary) { s.Retried++ })
		}
		attempts++
		res, err := r.s.pipeline.RunSlot(ctx, r.input(slot, attempts))
		r.record(res)
		if err != nil {
			r.slotError(slot, err)
			return
		}
		if res.Run.Outcome != schemas.OutcomeAbortedInfra {
			return
		}
	}
}

// settle reads the slot's runs. It reports done when a counted sealed run
// exists, when retries are exhausted, or when an interrupted run's agent had
// started (that run is sealed as infra_error and is the counted run).
// Otherwise it seals interrupted runs as aborted_infra and returns how many
// attempts the slot has used.
func (r *schedRun) settle(ctx context.Context, slot *schemas.Slot) (int, bool) {
	recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, slot.SlotID)
	if err != nil {
		r.slotError(slot, err)
		return 0, true
	}
	if r.skipDone(slot, recs) {
		return len(recs), true
	}
	for _, rec := range recs {
		if rec.Sealed {
			continue
		}
		res, err := r.s.pipeline.SealInterrupted(ctx, r.input(slot, rec.Run.Attempt), rec)
		r.record(res)
		if err != nil {
			r.slotError(slot, err)
			return len(recs), true
		}
		r.count(func(s *Summary) { s.Recovered++ })
		r.emit(Event{Kind: EventRunRecovered, SlotID: slot.SlotID, RunID: rec.Run.RunID, Outcome: res.Run.Outcome})
		if res.Run.Outcome != schemas.OutcomeAbortedInfra {
			return len(recs), true
		}
	}
	return len(recs), len(recs) >= maxAttempts
}

// skipDone reports, and counts as skipped, a slot that needs no new run: it
// has a counted sealed run, or every allowed attempt aborted and was sealed.
func (r *schedRun) skipDone(slot *schemas.Slot, recs []schemas.RunRecord) bool {
	sealedAborts := 0
	for _, rec := range recs {
		if !rec.Sealed {
			continue
		}
		if rec.Run.Outcome != schemas.OutcomeAbortedInfra {
			r.count(func(s *Summary) { s.Skipped++ })
			r.emit(Event{Kind: EventSlotSkipped, SlotID: slot.SlotID, RunID: rec.Run.RunID, Outcome: rec.Run.Outcome})
			return true
		}
		sealedAborts++
	}
	if sealedAborts >= maxAttempts {
		r.count(func(s *Summary) { s.Skipped++ })
		r.emit(Event{Kind: EventSlotSkipped, SlotID: slot.SlotID, Outcome: schemas.OutcomeAbortedInfra,
			Message: "every allowed attempt aborted on infrastructure: before its agent started, or with no model call answered because the provider was unreachable"})
		return true
	}
	return false
}

func (r *schedRun) input(slot *schemas.Slot, attempt int) SlotInput {
	return SlotInput{Manifest: r.m, Suite: r.suite, Slot: slot, Attempt: attempt, Emit: r.emit}
}

// record adds a finished run's cost and counts its seal.
func (r *schedRun) record(res *SlotResult) {
	if res == nil {
		return
	}
	c := 0.0
	if v, ok := res.Metrics[costMetric]; ok {
		c, _ = v.Float()
	}
	r.count(func(s *Summary) {
		s.SpentUSD += c
		if res.Seal != nil {
			s.Sealed++
		}
	})
}

func (r *schedRun) overBudget() bool {
	limit := r.m.Experiment.Budget.TotalUSD
	if limit <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sum.SpentUSD >= limit {
		r.stopped = true
		r.sum.BudgetStopped = true
	}
	return r.stopped
}

func (r *schedRun) slotError(slot *schemas.Slot, err error) {
	r.count(func(s *Summary) { s.Errors++ })
	r.s.logger.Error("slot failed", "experiment", r.m.ExpID, "slot", slot.SlotID, "error", err.Error())
	r.emit(Event{Kind: EventError, SlotID: slot.SlotID, Message: err.Error()})
}

func (r *schedRun) count(f func(*Summary)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.sum)
}

// emit sends without blocking, so a slow live view never stalls a run.
func (r *schedRun) emit(ev Event) {
	if r.opts.Events == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	select {
	case r.opts.Events <- ev:
	default:
		r.dropped()
	}
}

func (r *schedRun) dropped() {
	r.count(func(s *Summary) { s.DroppedEvents++ })
}

// heartbeat keeps the slot's lease fresh until stop is called.
func (r *schedRun) heartbeat(ctx context.Context, slotID string) (stop func()) {
	hctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(r.opts.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-hctx.Done():
				return
			case <-t.C:
				if err := r.s.reg.Store.Heartbeat(hctx, slotID, r.opts.Owner); err != nil && hctx.Err() == nil {
					r.s.logger.Warn("lease heartbeat", "slot", slotID, "error", err.Error())
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// slotRuns returns a slot's runs; a slot the store has no runs for yet is
// empty, not an error.
func slotRuns(ctx context.Context, store schemas.Store, expID, slotID string) ([]schemas.RunRecord, error) {
	recs, err := store.SlotRuns(ctx, expID, slotID)
	if errors.Is(err, schemas.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runs of slot %s: %w", slotID, err)
	}
	return recs, nil
}

// readCost returns the observed cost_usd in a run folder's metrics.json, or
// 0 when the run has no metrics (it aborted before measuring) or the cost
// is unknown.
func readCost(dir string) (float64, error) {
	b, err := os.ReadFile(filepath.Join(dir, "metrics.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read metrics: %w", err)
	}
	var vals map[string]schemas.Value
	if err := json.Unmarshal(b, &vals); err != nil {
		return 0, fmt.Errorf("parse %s: %w", filepath.Join(dir, "metrics.json"), err)
	}
	c, _ := vals[costMetric].Float()
	return c, nil
}
