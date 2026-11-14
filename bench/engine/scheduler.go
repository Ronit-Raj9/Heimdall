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
	"sync/atomic"
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

// ErrLeaseLost is the cause of a slot context the scheduler cancelled because
// the slot's lease was lost: another owner holds it, it is gone, or no
// heartbeat succeeded for longer than the stale threshold, after which any
// other scheduler may reclaim it.
var ErrLeaseLost = errors.New("slot lease lost")

// Scheduler leases slots and runs them through the pipeline with a fixed
// number of workers.
type Scheduler struct {
	reg       *schemas.Registry
	benchRoot string
	pipeline  *Pipeline
	janitor   *Janitor
	logger    schemas.Logger
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
	LeaseLost     int     `json:"lease_lost"`     // slots whose lease was lost mid-run; that run is sealed aborted_infra
	Pending       int     `json:"pending"`        // slots not leased because of the spend cap or cancellation
	Errors        int     `json:"errors"`         // slots that hit a store or seal error
	BudgetStopped bool    `json:"budget_stopped"` // the spend cap stopped leasing
	SpentUSD      float64 `json:"spent_usd"`
	// UnknownCostRuns counts runs whose agent started but whose cost could not
	// be observed; each is counted in SpentUSD at the per-run estimate.
	UnknownCostRuns int `json:"unknown_cost_runs"`
	DroppedEvents   int `json:"dropped_events"`
}

// schedRun is the shared state of one Run call. The spend fields are read
// and written under mu only, so a reservation, the cap check and a run's
// recorded cost never interleave.
type schedRun struct {
	s        *Scheduler
	m        *schemas.Manifest
	suite    *schemas.Suite
	opts     RunOptions
	mu       sync.Mutex
	sum      Summary
	stopped  bool          // spend cap reached
	reserved float64       // estimates set aside for slots in flight
	inflight int           // slots holding a reservation
	maxCost  float64       // highest observed cost of one run
	observed bool          // some run's cost was observed
	freed    chan struct{} // closed and replaced whenever a reservation is released
}

// NewScheduler returns a scheduler. janitor may be nil (tests, or a host
// without docker); then no sweep runs at start.
func NewScheduler(reg *schemas.Registry, benchRoot string, logger schemas.Logger, janitor *Janitor) *Scheduler {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &Scheduler{reg: reg, benchRoot: benchRoot, pipeline: NewPipeline(reg, benchRoot, logger), janitor: janitor, logger: logger}
}

// Run executes every slot of the manifest in OrderIndex order.
//
// Without Resume it writes the manifest to the store first and refuses an
// experiment that already exists. With Resume it requires the stored manifest
// to carry the same hash. Either way a slot with a counted sealed run is
// skipped, an interrupted run is sealed (infra_error when its agent started,
// aborted_infra otherwise, then a new run), and a run that aborts before its
// agent starts gets at most two retries.
//
// With Budget.TotalUSD set, a slot is leased only after a reservation
// (reserve): the spend so far plus every in-flight slot's estimate must be
// below the cap, and at or past the cap no further slot is leased. A slot
// whose lease is lost mid-run is cancelled, its run sealed as aborted_infra
// and never counted, and the slot is left to the owner that holds it.
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
			"spent_usd", out.SpentUSD, "pending", out.Pending, "unknown_cost_runs", out.UnknownCostRuns)
		r.emit(Event{Kind: EventBudgetStop, Message: fmt.Sprintf("spend cap %.2f USD reached at %.4f USD; %d slots not leased; %d runs had an unknown cost, counted at the per-run estimate",
			m.Experiment.Budget.TotalUSD, out.SpentUSD, out.Pending, out.UnknownCostRuns)})
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
		s.sweep(ctx, m.ExpID, opts.StaleAfter)
	}
	r := &schedRun{s: s, m: m, suite: suite, opts: opts, sum: Summary{ExpID: m.ExpID}, freed: make(chan struct{})}
	if err := r.spentSoFar(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// sweep runs the janitor over every run whose lease is not fresh: leases are
// read under the scheduler's store root and the agent check root, for every
// experiment and every owner, so a run another live process holds is kept.
// When the leases cannot be read nothing is swept, since such a run could
// not be told apart.
func (s *Scheduler) sweep(ctx context.Context, expID string, staleAfter time.Duration) {
	roots := []string{s.reg.Store.Root()}
	if s.benchRoot != "" {
		roots = append(roots, filepath.Join(s.benchRoot, ChecksRoot))
	}
	lv, err := ScanRunLiveness(roots, staleAfter, time.Now())
	if err != nil {
		s.logger.Warn("janitor skipped: leases could not be read", "experiment", expID, "error", err.Error())
		return
	}
	if _, err := s.janitor.Sweep(ctx, lv); err != nil {
		s.logger.Warn("janitor sweep failed", "experiment", expID, "error", err.Error())
	}
}

// spentSoFar sums the cost of the sealed runs of every slot into the
// summary and records the highest observed cost. A run whose cost is unknown
// is counted at the per-run estimate once every known cost is in.
func (r *schedRun) spentSoFar(ctx context.Context) error {
	unknown := 0
	for _, slot := range r.m.Slots {
		recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, slot.SlotID)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			if !rec.Sealed {
				continue
			}
			c, known, err := readCost(rec.Dir, &rec.Run)
			if err != nil {
				r.s.logger.Warn("unreadable metrics.json", "run", rec.Run.RunID, "error", err.Error())
			}
			if !known {
				unknown++
				continue
			}
			r.observeLocked(c)
		}
	}
	for range unknown {
		r.sum.SpentUSD += r.estimateLocked()
		r.sum.UnknownCostRuns++
	}
	return nil
}

// process runs one slot to a counted run, or decides it needs none.
//
// A slot that already has a counted sealed run is skipped before the budget
// check, so it is never reported as pending. The slot's reservation is held
// from before its lease until its last attempt is recorded.
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
	est, ok := r.reserve(ctx)
	if !ok {
		r.count(func(s *Summary) { s.Pending++ })
		return
	}
	defer r.release(est)
	store := r.s.reg.Store
	ok, err = store.Lease(ctx, slot.SlotID, r.opts.Owner, r.opts.StaleAfter)
	if err != nil {
		r.slotError(slot, fmt.Errorf("lease: %w", err))
		return
	}
	if !ok {
		r.count(func(s *Summary) { s.LeaseHeld++ })
		r.emit(Event{Kind: EventLeaseHeld, SlotID: slot.SlotID})
		return
	}
	sctx, stop := r.heartbeat(ctx, slot.SlotID)
	defer func() {
		if stop() {
			r.leaseLost(sctx, slot)
			return
		}
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := store.Release(rctx, slot.SlotID, r.opts.Owner); err != nil {
			r.s.logger.Warn("release lease", "slot", slot.SlotID, "error", err.Error())
		}
	}()
	r.attempts(sctx, slot, est)
}

// attempts settles the slot's earlier runs, then runs new attempts until one
// is not aborted_infra, the attempts are used up, or the slot's context ends
// (also when its lease is lost: that run is the last).
func (r *schedRun) attempts(ctx context.Context, slot *schemas.Slot, est float64) {
	attempts, done := r.settle(ctx, slot, est)
	if done {
		return
	}
	for attempts < maxAttempts && ctx.Err() == nil {
		if attempts > 0 {
			r.count(func(s *Summary) { s.Retried++ })
		}
		attempts++
		res, err := r.s.pipeline.RunSlot(ctx, r.input(slot, attempts))
		r.record(res, est)
		if err != nil {
			r.slotError(slot, err)
			return
		}
		if res.Run.Outcome != schemas.OutcomeAbortedInfra {
			return
		}
	}
}

// leaseLost counts and reports a slot whose lease was lost while it ran. The
// lease is not released: it is not this owner's any more.
func (r *schedRun) leaseLost(sctx context.Context, slot *schemas.Slot) {
	cause := context.Cause(sctx)
	r.count(func(s *Summary) { s.LeaseLost++ })
	r.s.logger.Error("slot lease lost: the slot's run was cancelled, and a run sealed after the loss is aborted_infra, never counted; the slot is left to the owner that holds it",
		"experiment", r.m.ExpID, "slot", slot.SlotID, "owner", r.opts.Owner, "cause", cause.Error())
	r.emit(Event{Kind: EventError, SlotID: slot.SlotID, Message: cause.Error()})
}

// settle reads the slot's runs. It reports done when a counted sealed run
// exists, when retries are exhausted, or when an interrupted run's agent had
// started (that run is sealed as infra_error and is the counted run).
// Otherwise it seals interrupted runs as aborted_infra and returns how many
// attempts the slot has used.
func (r *schedRun) settle(ctx context.Context, slot *schemas.Slot, est float64) (int, bool) {
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
		r.record(res, est)
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

// record adds a finished run's cost and counts its seal. A run whose agent
// started but whose cost is unknown is counted at est, the slot's
// reservation, or the per-run estimate when that is higher, never as zero.
func (r *schedRun) record(res *SlotResult, est float64) {
	if res == nil {
		return
	}
	c, known := runCost(res.Metrics, res.Run)
	r.mu.Lock()
	defer r.mu.Unlock()
	if known {
		r.observeLocked(c)
	} else {
		charge := max(est, r.estimateLocked())
		r.sum.SpentUSD += charge
		r.sum.UnknownCostRuns++
		r.s.logger.Warn("run cost unknown; counted at the per-run estimate", "experiment", r.m.ExpID,
			"run", res.Run.RunID, "counted_usd", charge)
	}
	if res.Seal != nil {
		r.sum.Sealed++
	}
}

// reserve sets aside one run's estimated cost before a slot is leased. With
// no cap it always succeeds and reserves nothing. Otherwise, under mu: at or
// past the cap the scheduler stops for good; below it, the slot starts when
// nothing is in flight or the spend so far plus every in-flight reservation
// is still below the cap, and else waits for an in-flight slot to finish (or
// ctx to end). So concurrent workers can never all pass one check, and the
// cap is passed by at most the last admitted run's error against its
// estimate.
func (r *schedRun) reserve(ctx context.Context) (float64, bool) {
	limit := r.m.Experiment.Budget.TotalUSD
	if limit <= 0 {
		return 0, true
	}
	for {
		r.mu.Lock()
		if !r.stopped && r.sum.SpentUSD >= limit {
			r.stopped, r.sum.BudgetStopped = true, true
		}
		if r.stopped {
			r.mu.Unlock()
			return 0, false
		}
		if r.inflight == 0 || r.sum.SpentUSD+r.reserved < limit {
			est := r.estimateLocked()
			r.reserved += est
			r.inflight++
			r.mu.Unlock()
			return est, true
		}
		wait := r.freed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, false
		case <-wait:
		}
	}
}

// release returns a reservation once the slot's last attempt is recorded and
// wakes every worker waiting in reserve.
func (r *schedRun) release(est float64) {
	if r.m.Experiment.Budget.TotalUSD <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reserved -= est
	r.inflight--
	if r.inflight <= 0 {
		r.inflight, r.reserved = 0, 0 // no float residue once nothing is in flight
	}
	close(r.freed)
	r.freed = make(chan struct{})
}

// estimateLocked is one run's expected cost: the highest cost observed so
// far, or budget.reserve_usd when that is higher. Before any cost is observed
// and with no reserve_usd it is the whole remaining budget, so the first run
// runs alone until its cost is known. The caller holds mu.
func (r *schedRun) estimateLocked() float64 {
	reserve := r.m.Experiment.Budget.ReserveUSD
	if r.observed || reserve > 0 {
		return max(r.maxCost, reserve)
	}
	return max(r.m.Experiment.Budget.TotalUSD-r.sum.SpentUSD, 0)
}

// observeLocked adds an observed run cost. The caller holds mu, or owns r
// alone (start).
func (r *schedRun) observeLocked(c float64) {
	r.sum.SpentUSD += c
	r.maxCost = max(r.maxCost, c)
	r.observed = true
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

// heartbeat keeps the slot's lease fresh and returns the slot's context. On
// lease loss (leaseLoss) it cancels that context with an ErrLeaseLost cause,
// so the pipeline stops the run and seals it as aborted_infra. stop ends the
// heartbeat and reports whether the lease was lost.
func (r *schedRun) heartbeat(ctx context.Context, slotID string) (context.Context, func() bool) {
	sctx, cancel := context.WithCancelCause(ctx)
	quit, done := make(chan struct{}), make(chan struct{})
	var lost atomic.Bool
	go func() {
		defer close(done)
		t := time.NewTicker(r.opts.HeartbeatEvery)
		defer t.Stop()
		last := time.Now()
		for {
			select {
			case <-quit:
				return
			case <-sctx.Done():
				return
			case <-t.C:
			}
			err := r.s.reg.Store.Heartbeat(sctx, slotID, r.opts.Owner)
			if sctx.Err() != nil {
				return
			}
			if err == nil {
				last = time.Now()
				continue
			}
			reason := leaseLoss(err, time.Since(last), r.opts.StaleAfter)
			if reason == "" {
				r.s.logger.Warn("lease heartbeat failed; retrying", "slot", slotID, "error", err.Error())
				continue
			}
			lost.Store(true)
			cancel(fmt.Errorf("%w: slot %s, owner %s: %s", ErrLeaseLost, slotID, r.opts.Owner, reason))
			return
		}
	}()
	return sctx, func() bool {
		close(quit)
		<-done
		cancel(nil)
		return lost.Load()
	}
}

// leaseLoss says why a failed heartbeat means the lease is lost, or "" when
// it may be transient: another owner holds the lease, there is no lease, or
// no heartbeat has succeeded for longer than staleAfter, after which any
// other scheduler may reclaim the slot. It is pure.
func leaseLoss(err error, sinceLast, staleAfter time.Duration) string {
	switch {
	case errors.Is(err, schemas.ErrLeaseHeld):
		return "another owner holds the lease: " + err.Error()
	case errors.Is(err, schemas.ErrNotFound):
		return "the lease is gone: " + err.Error()
	case sinceLast > staleAfter:
		return fmt.Sprintf("no heartbeat succeeded for %s, past the stale threshold %s: %s",
			sinceLast.Round(time.Second), staleAfter, err.Error())
	}
	return ""
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

// readCost returns the cost_usd in a run folder's metrics.json and whether
// it is known (runCost). A run without metrics.json aborted before
// measuring; an unreadable file leaves the cost to runCost without metrics.
func readCost(dir string, run *schemas.Run) (float64, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "metrics.json"))
	if errors.Is(err, fs.ErrNotExist) {
		c, known := runCost(nil, run)
		return c, known, nil
	}
	var vals map[string]schemas.Value
	if err == nil {
		if err = json.Unmarshal(b, &vals); err != nil {
			err = fmt.Errorf("parse %s: %w", filepath.Join(dir, "metrics.json"), err)
		}
	} else {
		err = fmt.Errorf("read metrics: %w", err)
	}
	c, known := runCost(vals, run)
	return c, known, err
}

// runCost is a run's observed cost_usd. A run whose agent never started made
// no model call, so its cost is a known 0 even with no metrics. Any other
// run without an observed cost has an unknown cost. It is pure.
func runCost(metrics map[string]schemas.Value, run *schemas.Run) (float64, bool) {
	if c, ok := metrics[costMetric].Float(); ok {
		return c, true
	}
	if run == nil || run.Timing.AgentStarted == nil {
		return 0, true
	}
	return 0, false
}
