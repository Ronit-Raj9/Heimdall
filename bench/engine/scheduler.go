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
	suitepkg "github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Scheduler defaults.
const (
	DefaultConcurrency    = 2
	DefaultStaleAfter     = 2 * time.Minute
	DefaultHeartbeatEvery = 15 * time.Second
	costMetric            = "cost_usd"
	// simulatorCostMetric is the model-played user's own spend
	// (metrics.SimulatorCostUSD). It is never part of cost_usd, but it is
	// spend, so the budget counts it (runCost).
	simulatorCostMetric = "simulator_cost_usd"
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
	// Remote, when set, opens the experiment to remote workers for the
	// duration of Run (remote.go): they lease slots from the same queue and
	// reserve against the same spend cap as the local workers.
	Remote *Coordinator
	// RemoteOnly runs no local worker: every slot waits for a remote worker,
	// and Run returns once none is pending or out, or ctx ends. Hosted mode
	// sets it, since strangers' agents never run on the hosted server. It
	// needs Remote. (Runners area, docs/runners.md)
	RemoteOnly bool
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
	// Remote counts runs remote workers sealed that the coordinator accepted;
	// they are in Sealed too. RemoteRejected counts uploads it refused.
	Remote         int `json:"remote,omitempty"`
	RemoteRejected int `json:"remote_rejected,omitempty"`
	// W7 adaptive trials (adaptive_sched.go): optional slots the frozen rule
	// did not release (not required, not missing) and why it stopped.
	NotRequired int    `json:"not_required,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// schedRun is the shared state of one Run call. The spend fields are read
// and written under mu only, so a reservation, the cap check and a run's
// recorded cost never interleave.
type schedRun struct {
	s        *Scheduler
	m        *schemas.Manifest
	suite    *schemas.Suite
	opts     RunOptions
	ctx      context.Context // Run's context, for work done on behalf of remote workers
	queue    *slotQueue
	mu       sync.Mutex
	sum      Summary
	stopped  bool          // spend cap reached
	reserved float64       // estimates set aside for slots in flight
	inflight int           // slots holding a reservation
	maxCost  float64       // highest observed cost of one run
	observed bool          // some run's cost was observed
	freed    chan struct{} // closed and replaced whenever a reservation is released

	w7 *adaptiveState // W7 adaptive trials: slots queued and the last replay (adaptive_sched.go)
	// X7: the GPUs each GPU worker holds, by slot id (gpu.go).
	gpuMu sync.Mutex
	gpus  map[string]*schemas.GPUAssignment
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
//
// With opts.Remote set, remote workers lease slots from the same queue and
// reserve against the same cap while Run lasts, and Run returns only when no
// slot is pending and none is out on a remote lease (or ctx ends).
func (s *Scheduler) Run(ctx context.Context, m *schemas.Manifest, opts RunOptions) (*Summary, error) {
	if opts.RemoteOnly && opts.Remote == nil { // Runners area
		return nil, errors.New("scheduler: remote only needs a coordinator")
	}
	r, err := s.start(ctx, m, opts)
	if err != nil {
		return nil, err
	}
	r.ctx = ctx
	r.queue = newSlotQueue(initialSlots(m)) // W7 adaptive trials: optional slots wait for the rule (adaptive_sched.go)
	detach := func() {}
	if opts.Remote != nil {
		detach = opts.Remote.attach(r)
	}
	workers := opts.Concurrency
	if workers <= 0 {
		workers = m.Experiment.Concurrency
	}
	if workers <= 0 {
		workers = DefaultConcurrency
	}
	// X7: GPU slots go to GPU workers only, one per GPU of the pool, and
	// never to a CPU worker (gpu.go). With remote workers attached, a CPU
	// worker also waits while GPU slots are pending, since a remote GPU
	// worker may still take them.
	nGPU := gpuSlots(m, r.suite)
	gpuWorkerCount := gpuWorkers(r.s.reg, nGPU)
	if nGPU > 0 && gpuWorkerCount == 0 && opts.Remote == nil {
		detach()
		return nil, fmt.Errorf("run %s: %d slots are of GPU tasks and this machine has no GPU pool (bifrost-bench gpu check); nothing was run", m.ExpID, nGPU)
	}
	if nGPU > 0 && gpuWorkerCount == 0 {
		r.emit(Event{Kind: EventError, Message: fmt.Sprintf("%d slots are of GPU tasks and this machine has no GPU pool: they wait for a remote worker that registered a GPU", nGPU)})
	}
	isGPU := func(s *schemas.Slot) bool { return slotNeedsGPU(r.suite, s) }
	isCPU := func(s *schemas.Slot) bool { return !slotNeedsGPU(r.suite, s) }
	if opts.RemoteOnly { // Runners area: no local worker
		workers, gpuWorkerCount = 0, 0
	}
	for more := true; more; more = r.releaseOptional(ctx) { // W7 adaptive trials: one stage per release of the rule (adaptive_sched.go)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					slot, ok := r.queue.nextWhere(ctx, isCPU, opts.Remote != nil && nGPU > 0) // X7: CPU slots only
					if !ok {
						return
					}
					r.process(ctx, slot)
				}
			}()
		}
		for range gpuWorkerCount { // X7: one GPU worker per GPU (gpu.go)
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					slot, ok := r.queue.nextWhere(ctx, isGPU, false)
					if !ok {
						return
					}
					r.runGPUSlot(ctx, slot)
				}
			}()
		}
		wg.Wait()
		if opts.RemoteOnly { // Runners area: wait for the remote workers before the rule looks at the stage
			r.queue.drained(ctx)
		}
	}
	detach()
	r.mu.Lock()
	out, stopped := r.sum, r.stopped
	r.mu.Unlock()
	r.adaptiveSummary(&out) // W7
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
	if !suitepkg.LockHashMatches(suite, m.SuiteLockHash) { // Lock fingerprints: a lock that only gained fingerprints still matches (fingerprints.go)
		return nil, fmt.Errorf("suite %s lock hash %q differs from the manifest's %q", suite.ID, suite.LockHash, m.SuiteLockHash)
	}
	if err := checkFingerprints(ctx, s.reg, suite, s.logger, m.Experiment.Smoke, "run", m.ExpID); err != nil {
		return nil, fmt.Errorf("run %s: %w", m.ExpID, err)
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
			c, known, err := readSpend(rec.Dir, &rec.Run)
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
	charges, err := r.remoteCharges()
	if err != nil {
		return err
	}
	for _, ch := range charges {
		if ch.CostKnown {
			r.observeLocked(*ch.ChargedUSD)
			continue
		}
		r.sum.SpentUSD += *ch.ChargedUSD
		r.sum.UnknownCostRuns++
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
	budget := maxAttempts + r.stoppedRuns(ctx, slot) // Jobs area: a run its owner stopped (cancel, shutdown) uses no attempt (cancel.go)
	for attempts < budget && ctx.Err() == nil {
		if r.capReached() { // every charge so far is recorded: a retry or a run after recovery never starts past the cap
			r.count(func(s *Summary) { s.Pending++ })
			return
		}
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
	return len(recs), len(recs)-countOwnerStopped(recs) >= maxAttempts // Jobs area: owner-stopped runs use no attempt (cancel.go)
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
		if !OwnerStopped(&rec.Run) { // Jobs area: a run its owner stopped uses no attempt (cancel.go)
			sealedAborts++
		}
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
	return SlotInput{Manifest: r.m, Suite: r.suite, Slot: slot, Attempt: attempt, Emit: r.emit, GPU: r.gpuFor(slot.SlotID)} // X7: GPU
}

// record adds a finished run's cost and counts its seal. A run whose agent
// started but whose cost is unknown is counted at est, the slot's
// reservation, or the per-run estimate when that is higher, never as zero.
func (r *schedRun) record(res *SlotResult, est float64) {
	if res == nil {
		return
	}
	c, known := spendCost(res.Dir, res.Metrics, res.Run)
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
	if r.m.Experiment.Budget.TotalUSD <= 0 {
		return 0, true
	}
	for {
		r.mu.Lock()
		est, ok, wait := r.reserveLocked()
		if !wait {
			r.mu.Unlock()
			return est, ok
		}
		freed := r.freed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, false
		case <-freed:
		}
	}
}

// tryReserve is reserve for a remote lease request, which must not block:
// where reserve would wait for an in-flight slot, it reports wait and the
// request is answered with no work for now.
func (r *schedRun) tryReserve() (est float64, ok, wait bool) {
	if r.m.Experiment.Budget.TotalUSD <= 0 {
		return 0, true, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reserveLocked()
}

// reserveLocked is one pass of the reservation rule under mu: at or past the
// cap stop for good; below it reserve when nothing is in flight or the spend
// plus every in-flight reservation is below the cap; else report wait.
func (r *schedRun) reserveLocked() (est float64, ok, wait bool) {
	limit := r.m.Experiment.Budget.TotalUSD
	if !r.stopped && r.sum.SpentUSD >= limit {
		r.stopped, r.sum.BudgetStopped = true, true
	}
	if r.stopped {
		return 0, false, false
	}
	if r.inflight == 0 || r.sum.SpentUSD+r.reserved < limit {
		est = r.estimateLocked()
		r.reserved += est
		r.inflight++
		return est, true, false
	}
	return 0, false, true
}

// capReached reports, under mu, whether the spend recorded so far is at or
// past the cap, and then stops leasing for good. With no cap it is false. It
// is checked before every new attempt on a slot that already holds a
// reservation, since that reservation was sized for one run, not a retry.
func (r *schedRun) capReached() bool {
	limit := r.m.Experiment.Budget.TotalUSD
	if limit <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sum.SpentUSD >= limit {
		r.stopped, r.sum.BudgetStopped = true, true
	}
	return r.stopped
}

// charge adds the cost of a run that ran on a remote worker but was never
// sealed here (a rejected upload, a lost or abandoned lease): the cost the
// worker reported when it was observed, else the slot's reservation or the
// per-run estimate when that is higher, never zero. It returns what it
// counted and whether that was an observed cost, so the coordinator can
// journal the charge and a resumed scheduler can count it again
// (remoteCharges).
func (r *schedRun) charge(cost *schemas.Value, est float64, why string) (float64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cost != nil {
		if c, ok := cost.Float(); ok && c >= 0 {
			r.observeLocked(c)
			return c, true
		}
	}
	counted := max(est, r.estimateLocked())
	r.sum.SpentUSD += counted
	r.sum.UnknownCostRuns++
	r.s.logger.Warn("remote run cost unknown; counted at the per-run estimate", "experiment", r.m.ExpID,
		"why", why, "counted_usd", counted)
	return counted, false
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
// far, or budget.reserve_usd when that is higher. Before any positive cost is
// observed and with no reserve_usd it is the whole remaining budget, so runs
// go one at a time until a positive cost is known. An observed zero (a run
// whose agent never started, or a free run) never makes the estimate zero,
// since an unknown cost charged at zero would leave the cap untouched. The
// caller holds mu.
func (r *schedRun) estimateLocked() float64 {
	reserve := r.m.Experiment.Budget.ReserveUSD
	if (r.observed && r.maxCost > 0) || reserve > 0 {
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

// runCost is a run's observed spend for the budget: cost_usd plus the
// model-played user's simulator_cost_usd when that applies. A run whose
// agent never started made no model call, so its cost is a known 0 even with
// no metrics. Any other run without an observed cost_usd, or whose simulator
// spend applies but was not observed, has an unknown cost. It is pure.
func runCost(metrics map[string]schemas.Value, run *schemas.Run) (float64, bool) {
	c, ok := metrics[costMetric].Float()
	if !ok {
		if run == nil || run.Timing.AgentStarted == nil {
			return 0, true
		}
		return 0, false
	}
	if v, present := metrics[simulatorCostMetric]; present && v.State != "" && v.State != schemas.ValueNotApplicable {
		sim, ok := v.Float()
		if !ok {
			return 0, false
		}
		c += sim
	}
	return c, true
}

// judgeCost is what the run's judge model calls cost, from grade.json in the
// run folder: 0 when the run has no grade or was not judged, unknown when a
// judge voted but its cost was not observed. The judge's cost is never part
// of cost_usd, but it is spend, so the budget counts it.
func judgeCost(dir string) (float64, bool) {
	if dir == "" {
		return 0, true
	}
	b, err := os.ReadFile(filepath.Join(dir, "grade.json"))
	if err != nil {
		return 0, errors.Is(err, fs.ErrNotExist)
	}
	var g struct {
		Judge *struct {
			USD schemas.Value `json:"usd"`
		} `json:"judge"`
	}
	if json.Unmarshal(b, &g) != nil {
		return 0, false
	}
	if g.Judge == nil || g.Judge.USD.State == "" || g.Judge.USD.State == schemas.ValueNotApplicable {
		return 0, true
	}
	return g.Judge.USD.Float()
}

// spendCost is a finished run's whole spend for the budget: runCost plus
// judgeCost. Either part unknown makes the whole unknown.
func spendCost(dir string, metrics map[string]schemas.Value, run *schemas.Run) (float64, bool) {
	c, ok := runCost(metrics, run)
	if !ok {
		return 0, false
	}
	if run == nil || run.Timing.AgentStarted == nil {
		return c, true // no agent, no grade: nothing was judged
	}
	j, ok := judgeCost(dir)
	if !ok {
		return 0, false
	}
	return c + j, true
}

// readSpend is readCost plus the run's judge cost (spendCost).
func readSpend(dir string, run *schemas.Run) (float64, bool, error) {
	c, known, err := readCost(dir, run)
	if !known {
		return 0, false, err
	}
	if run == nil || run.Timing.AgentStarted == nil {
		return c, true, err
	}
	j, ok := judgeCost(dir)
	if !ok {
		return 0, false, err
	}
	return c + j, true, err
}

// slotQueue hands the experiment's slots out in OrderIndex order, to the
// local workers and, while a coordinator is attached, to remote workers. A
// slot a remote worker gives back without a finished run (a lost lease, a
// rejected upload, an aborted attempt) is queued again in its place.
type slotQueue struct {
	mu      sync.Mutex
	pending []*schemas.Slot
	remote  int           // slots out on remote leases
	changed chan struct{} // closed and replaced whenever pending or remote changes
}

func newSlotQueue(slots []schemas.Slot) *slotQueue {
	sorted := append([]schemas.Slot(nil), slots...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OrderIndex < sorted[j].OrderIndex })
	q := &slotQueue{changed: make(chan struct{}), pending: make([]*schemas.Slot, 0, len(sorted))}
	for i := range sorted {
		q.pending = append(q.pending, &sorted[i])
	}
	return q
}

// next returns the next slot for a local worker. While remote workers hold
// slots it waits for one to come back or finish, so Run never returns with
// work out; it reports false when nothing is pending and nothing is out, or
// when ctx ends while it waits.
func (q *slotQueue) next(ctx context.Context) (*schemas.Slot, bool) {
	return q.nextWhere(ctx, nil, false)
}

// nextWhere is next for the slots match accepts (nil accepts every slot;
// X7: CPU and GPU workers each take their own kind). Besides waiting while
// remote workers hold slots, it waits while other slots are pending when
// waitOthers is set, so a local worker stays until a remote worker has taken
// the slots it cannot run.
func (q *slotQueue) nextWhere(ctx context.Context, match func(*schemas.Slot) bool, waitOthers bool) (*schemas.Slot, bool) {
	for {
		q.mu.Lock()
		for i, slot := range q.pending {
			if match == nil || match(slot) {
				q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
				q.notifyLocked() // X7: a worker waiting on other slots re-checks
				q.mu.Unlock()
				return slot, true
			}
		}
		if q.remote == 0 && (!waitOthers || len(q.pending) == 0) {
			q.mu.Unlock()
			return nil, false
		}
		wait := q.changed
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false
		case <-wait:
		}
	}
}

// drained waits until no slot is pending and none is out, or ctx ends. A
// remote-only run waits here while remote workers take every slot (Runners
// area).
func (q *slotQueue) drained(ctx context.Context) {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 && q.remote == 0 {
			q.mu.Unlock()
			return
		}
		wait := q.changed
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-wait:
		}
	}
}

// take pops the next slot for a remote worker without waiting. The slot is
// out until giveBack or finish.
func (q *slotQueue) take() (*schemas.Slot, bool) {
	return q.takeWhere(nil)
}

// takeWhere is take for the first pending slot match accepts (nil accepts
// every slot; X7: a remote worker without free GPUs takes no GPU slot).
func (q *slotQueue) takeWhere(match func(*schemas.Slot) bool) (*schemas.Slot, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, slot := range q.pending {
		if match == nil || match(slot) {
			q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
			q.remote++
			q.notifyLocked()
			return slot, true
		}
	}
	return nil, false
}

// giveBack queues a slot that was out again, in OrderIndex order.
func (q *slotQueue) giveBack(slot *schemas.Slot) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := sort.Search(len(q.pending), func(i int) bool { return q.pending[i].OrderIndex > slot.OrderIndex })
	q.pending = append(q.pending, nil)
	copy(q.pending[i+1:], q.pending[i:])
	q.pending[i] = slot
	q.remote--
	q.notifyLocked()
}

// finish ends a slot that was out and needs nothing more from this queue.
func (q *slotQueue) finish() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.remote--
	q.notifyLocked()
}

func (q *slotQueue) notifyLocked() {
	close(q.changed)
	q.changed = make(chan struct{})
}
