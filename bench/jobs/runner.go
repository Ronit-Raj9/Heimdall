package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Handler runs one job. ctx is cancelled with cause ErrCancelled when a
// cancel is requested, ErrShutdown when the runner stops and ErrLeaseLost
// when another runner reclaimed the job. progress stores its argument, as
// JSON, as the job's latest progress. The result is stored as JSON.
type Handler func(ctx context.Context, j *Job, log Logger, progress func(any)) (result any, err error)

// Validator checks a job's params for an organization before the job is
// queued. It returns the params to store (nil keeps them as given) and the
// job's target ("" for none: then any number may run at once).
type Validator func(ctx context.Context, orgID string, params json.RawMessage) (normalized any, target string, err error)

// KindOption configures a registered kind.
type KindOption func(*kind)

// WithValidator sets the kind's params validator.
func WithValidator(v Validator) KindOption { return func(k *kind) { k.validate = v } }

// WithMaxAttempts caps how often the kind's jobs are claimed: a job whose
// lease expired after its last allowed attempt fails instead of being queued
// again.
func WithMaxAttempts(n int) KindOption { return func(k *kind) { k.maxAttempts = n } }

// Internal marks a kind the API may not create (Create refuses it); the
// server enqueues it itself with Enqueue.
func Internal() KindOption { return func(k *kind) { k.internal = true } }

// WithLimit caps how many of the kind's jobs this runner runs at once
// (total) and how many of them belong to one organization (perOrg); 0 means
// no cap. A kind whose handlers can wait a long time (an experiment waiting
// for a runner) uses it so it can not take every worker: the other kinds
// and other organizations keep progressing.
func WithLimit(total, perOrg int) KindOption {
	return func(k *kind) { k.limit, k.orgLimit = total, perOrg }
}

// WithCancelHook sets a function called after a queued job of the kind was
// cancelled before any runner started it, whichever path cancelled it (the
// generic job API or the kind's own API): no handler runs to clean up after
// such a job, so state kept for it outside the database is released here.
func WithCancelHook(f func(j *Job)) KindOption { return func(k *kind) { k.onCancel = f } }

// WithClaimGate makes the kind's queued jobs claimable only for the
// organizations gate returns at claim time. A kind whose handler would only
// wait for outside capacity (a hosted experiment with no live runner) uses
// it so such a job stays queued instead of holding a worker, and an
// organization that has capacity is not starved behind it. The jobs are
// claimed once the gate lists their organization; the claim loop polls.
func WithClaimGate(gate func() []string) KindOption { return func(k *kind) { k.gate = gate } }

type kind struct {
	gate        func() []string
	onCancel    func(j *Job)
	name        string
	handler     Handler
	validate    Validator
	maxAttempts int
	limit       int // running jobs of the kind at once; 0: no cap
	orgLimit    int // running jobs of the kind per organization; 0: no cap
	internal    bool
}

// Options configure a Runner. Zero values take the defaults.
type Options struct {
	Workers        int           // jobs run at once by this runner; default 4
	Owner          string        // lease owner; default host-pid-random
	LeaseFor       time.Duration // default 30s
	HeartbeatEvery time.Duration // default LeaseFor/3
	PollEvery      time.Duration // claim and cancel poll; default 1s
	MaxAttempts    int           // default 3, per kind with WithMaxAttempts
	LogHead        int           // first lines kept per job; default 500
	LogTail        int           // newest lines kept per job; default 1500
	MaxLineBytes   int           // longer lines are cut; default 8192
	ProgressEvery  time.Duration // progress writes at most this often; default 500ms
	ShutdownGrace  time.Duration // Stop waits this long for handlers; default 30s
	Logger         schemas.Logger
}

func (o Options) withDefaults() Options {
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.Owner == "" {
		host, _ := os.Hostname()
		o.Owner = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), db.NewID("")[20:])
	}
	if o.LeaseFor <= 0 {
		o.LeaseFor = 30 * time.Second
	}
	if o.HeartbeatEvery <= 0 {
		o.HeartbeatEvery = o.LeaseFor / 3
	}
	if o.PollEvery <= 0 {
		o.PollEvery = time.Second
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	if o.LogHead <= 0 {
		o.LogHead = 500
	}
	if o.LogTail <= 0 {
		o.LogTail = 1500
	}
	if o.MaxLineBytes <= 0 {
		o.MaxLineBytes = 8192
	}
	if o.ProgressEvery <= 0 {
		o.ProgressEvery = 500 * time.Millisecond
	}
	if o.ShutdownGrace <= 0 {
		o.ShutdownGrace = 30 * time.Second
	}
	if o.Logger == nil {
		o.Logger = schemas.NoopLogger{}
	}
	return o
}

// Runner claims queued jobs of its registered kinds and runs them. It is
// also the API's entry point: Create validates against the registered kinds.
type Runner struct {
	*Store
	opts Options

	mu      sync.Mutex
	kinds   map[string]*kind
	running map[string]context.CancelCauseFunc
	started bool
	stopped bool
	wake    chan struct{}
	quit    chan struct{}
	halted  chan struct{}     // closed when the first Stop has finished waiting
	active  map[string]int    // running jobs per kind
	orgAct  map[[2]string]int // running jobs per kind and organization
	claimMu sync.Mutex        // one claim at a time, so the kind limits hold
	idle    atomic.Int64      // unix nanos until which a claim is skipped: the last one found nothing (fairqueue.go)
	wg      sync.WaitGroup    // workers and the sweeper
	jobsWG  sync.WaitGroup    // running handlers
}

// New returns a runner over d. Register kinds, then Start it.
func New(d *db.DB, opts Options) *Runner {
	return &Runner{
		Store: NewStore(d), opts: opts.withDefaults(),
		kinds: map[string]*kind{}, running: map[string]context.CancelCauseFunc{},
		wake: make(chan struct{}, 1), quit: make(chan struct{}), halted: make(chan struct{}),
		active: map[string]int{}, orgAct: map[[2]string]int{},
	}
}

// Owner returns the lease owner this runner claims as.
func (r *Runner) Owner() string { return r.opts.Owner }

// Register adds a job kind. Registering a kind twice replaces it.
func (r *Runner) Register(name string, h Handler, opts ...KindOption) {
	k := &kind{name: name, handler: h, maxAttempts: r.opts.MaxAttempts}
	for _, o := range opts {
		o(k)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kinds[name] = k
}

// Kinds lists the registered kinds the API may create, sorted.
func (r *Runner) Kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for n, k := range r.kinds {
		if !k.internal {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (r *Runner) kind(name string) (*kind, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.kinds[name]
	return k, ok
}

// Cancel requests cancellation (Store.Cancel) and runs the kind's cancel
// hook when the job was cancelled before it started.
func (r *Runner) Cancel(ctx context.Context, orgID, id string) (*Job, error) {
	j, err := r.Store.Cancel(ctx, orgID, id)
	if err != nil || j.Status != StatusCancelled {
		return j, err
	}
	if k, ok := r.kind(j.Kind); ok && k.onCancel != nil {
		k.onCancel(j)
	}
	return j, nil
}

// NewJob is a job to queue.
type NewJob struct {
	OrgID     string
	Kind      string
	Params    json.RawMessage // nil means {}
	CreatedBy string          // user, token or "system"
	Priority  int             // claim order among equally loaded orgs (fairqueue.go); clamped to MinPriority..MaxPriority
}

// Create validates and queues a job the API asked for: the kind must be
// registered and not internal.
func (r *Runner) Create(ctx context.Context, n NewJob) (*Job, error) {
	k, ok := r.kind(n.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, n.Kind)
	}
	if k.internal {
		return nil, fmt.Errorf("%w: %s", ErrNotCreatable, n.Kind)
	}
	return r.enqueue(ctx, k, n)
}

// Enqueue validates and queues a job of any registered kind, internal ones
// included. The server uses it for experiment runs and regrades.
func (r *Runner) Enqueue(ctx context.Context, n NewJob) (*Job, error) {
	k, ok := r.kind(n.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, n.Kind)
	}
	return r.enqueue(ctx, k, n)
}

func (r *Runner) enqueue(ctx context.Context, k *kind, n NewJob) (*Job, error) {
	if strings.TrimSpace(n.OrgID) == "" {
		return nil, errors.New("job has no organization")
	}
	params := n.Params
	if len(strings.TrimSpace(string(params))) == 0 {
		params = json.RawMessage("{}")
	}
	if !json.Valid(params) {
		return nil, fmt.Errorf("%w: params are not JSON", ErrInvalidParams)
	}
	target := ""
	if k.validate != nil {
		norm, t, err := k.validate(ctx, n.OrgID, params)
		if err != nil {
			if errors.Is(err, ErrInvalidParams) || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
		}
		target = t
		if norm != nil {
			b, err := json.Marshal(norm)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
			}
			params = b
		}
	}
	j := &Job{ID: db.NewID("job"), OrgID: n.OrgID, Kind: k.name, Target: target, Status: StatusQueued,
		Params: params, CreatedBy: n.CreatedBy, CreatedAt: r.now().UTC(), Priority: clampPriority(n.Priority)}
	if err := r.insert(ctx, j); err != nil {
		return nil, err
	}
	r.poke()
	return r.Get(ctx, n.OrgID, j.ID)
}

// poke wakes one idle worker so a new job starts without waiting a poll.
func (r *Runner) poke() {
	r.clearIdle()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Start launches the workers and the lease sweeper. Recovery of jobs whose
// runner died happens on the sweeper's first pass, right away.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("job runner already started")
	}
	r.started = true
	r.mu.Unlock()
	if _, err := r.Recover(ctx); err != nil {
		r.opts.Logger.Warn("job recovery failed", "error", err.Error())
	}
	r.wg.Add(1)
	go r.sweeper()
	for range r.opts.Workers {
		r.wg.Add(1)
		go r.worker()
	}
	return nil
}

// Stop stops claiming, cancels every running handler with ErrShutdown, waits
// up to the grace period (or ctx) for them, and queues their jobs again so
// the next runner resumes them. A second call waits (up to its ctx) for the
// first one to finish, so a caller that closes the database after Stop
// returns never races the hand-back of another caller's stop.
func (r *Runner) Stop(ctx context.Context) {
	r.mu.Lock()
	if !r.started {
		if !r.stopped {
			r.stopped = true
			close(r.halted)
		}
		r.mu.Unlock()
		return
	}
	if r.stopped {
		r.mu.Unlock()
		select {
		case <-r.halted:
		case <-ctx.Done():
		}
		return
	}
	r.stopped = true
	close(r.quit)
	for _, cancel := range r.running {
		cancel(ErrShutdown)
	}
	r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		r.jobsWG.Wait()
		r.wg.Wait()
		close(done)
	}()
	grace := time.NewTimer(r.opts.ShutdownGrace)
	defer grace.Stop()
	defer close(r.halted)
	select {
	case <-done:
	case <-grace.C:
		r.opts.Logger.Warn("job runner stopped with handlers still running; their leases expire and another runner resumes them")
	case <-ctx.Done():
	}
}

func (r *Runner) isStopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

func (r *Runner) worker() {
	defer r.wg.Done()
	t := time.NewTicker(r.opts.PollEvery)
	defer t.Stop()
	for {
		if r.isStopped() {
			return
		}
		j, err := r.claim(context.Background())
		if err != nil {
			r.opts.Logger.Warn("claim job failed", "error", err.Error())
		}
		if j != nil {
			r.run(j)
			r.release(j)
			continue
		}
		select {
		case <-r.quit:
			return
		case <-r.wake:
		case <-t.C:
		}
	}
}

func (r *Runner) sweeper() {
	defer r.wg.Done()
	every := max(r.opts.LeaseFor/3, r.opts.PollEvery)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-r.quit:
			return
		case <-t.C:
		}
		if _, err := r.Recover(context.Background()); err != nil {
			r.opts.Logger.Warn("job recovery failed", "error", err.Error())
		}
	}
}

// claim takes the oldest queued job of a registered kind with db.ClaimOne:
// one UPDATE ... RETURNING whose candidate SELECT runs under the write lock
// (SQLite) or FOR UPDATE SKIP LOCKED (Postgres), so of two runners racing for
// one job exactly one gets it.
func (r *Runner) claim(ctx context.Context) (*Job, error) {
	r.claimMu.Lock()
	defer r.claimMu.Unlock()
	if r.queueIdle() { // another worker just found nothing (fairqueue.go)
		return nil, nil
	}
	r.mu.Lock()
	names := make([]string, 0, len(r.kinds))
	full := map[string][]string{} // kind -> organizations at the kind's per-org cap
	gates := map[string]func() []string{}
	for n, k := range r.kinds {
		if k.limit > 0 && r.active[n] >= k.limit {
			continue
		}
		if k.gate != nil {
			gates[n] = k.gate
		}
		names = append(names, n)
		if k.orgLimit > 0 {
			for key, c := range r.orgAct {
				if key[0] == n && c >= k.orgLimit {
					full[n] = append(full[n], key[1])
				}
			}
		}
	}
	r.mu.Unlock()
	open := map[string][]string{} // gated kind -> organizations its gate lets through
	for n, gate := range gates {
		orgs := gate()
		if len(orgs) == 0 {
			names = slices.DeleteFunc(names, func(x string) bool { return x == n })
			continue
		}
		open[n] = orgs
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")
	// A queued row with a cancel request is never handed to a handler.
	where := "status = ? AND cancel_requested = 0 AND kind IN (" + marks + ")"
	args := []any{string(StatusQueued)}
	for _, n := range names {
		args = append(args, n)
	}
	for _, n := range names {
		orgs := full[n]
		if len(orgs) == 0 {
			continue
		}
		sort.Strings(orgs)
		where += " AND NOT (kind = ? AND org_id IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(orgs)), ", ") + "))"
		args = append(args, n)
		for _, o := range orgs {
			args = append(args, o)
		}
	}
	for _, n := range names {
		orgs := open[n]
		if len(orgs) == 0 {
			continue
		}
		sort.Strings(orgs)
		where += " AND NOT (kind = ? AND org_id NOT IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(orgs)), ", ") + "))"
		args = append(args, n)
		for _, o := range orgs {
			args = append(args, o)
		}
	}
	now := r.now()
	row, err := r.db.ClaimOne(ctx, db.ClaimQuery{
		Table:     "jobs",
		Where:     where,
		Args:      args,
		OrderBy:   fairOrder, // per-org round robin, then priority, then age (fairqueue.go)
		Set:       "status = ?, lease_owner = ?, lease_until = ?, attempt = attempt + 1, started_at = COALESCE(started_at, ?)",
		SetArgs:   []any{string(StatusRunning), r.opts.Owner, now.Add(r.opts.LeaseFor).UnixMilli(), now.UnixMilli()},
		Returning: jobColumns,
	})
	if err != nil {
		return nil, err
	}
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		r.markIdle()
		return nil, nil
	}
	if err == nil {
		r.mu.Lock()
		r.active[j.Kind]++
		r.orgAct[[2]string{j.Kind, j.OrgID}]++
		r.mu.Unlock()
	}
	return j, err
}

// release returns the claim's place under the kind limits once the job
// ended (or was handed back) on this runner.
func (r *Runner) release(j *Job) {
	r.clearIdle() // a freed place under a cap can make a queued job claimable
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[j.Kind]--; r.active[j.Kind] <= 0 {
		delete(r.active, j.Kind)
	}
	key := [2]string{j.Kind, j.OrgID}
	if r.orgAct[key]--; r.orgAct[key] <= 0 {
		delete(r.orgAct, key)
	}
}

// RecoverResult counts what Recover did.
type RecoverResult struct {
	Requeued  int `json:"requeued"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// Recover settles running jobs whose lease expired (their runner died or
// hung): a job with a cancel request is cancelled, a job out of attempts
// fails, any other is queued again, and its next claim counts an attempt.
// Only kinds registered on this runner are settled: their attempt limit is
// per kind, and a runner without the kind (an -offline server) can not know
// it, so it leaves those jobs to a runner that has it.
func (r *Runner) Recover(ctx context.Context) (RecoverResult, error) {
	var out RecoverResult
	now := r.now().UnixMilli()
	r.mu.Lock()
	names := make([]string, 0, len(r.kinds))
	for n := range r.kinds {
		names = append(names, n)
	}
	r.mu.Unlock()
	if len(names) == 0 {
		return out, nil
	}
	sort.Strings(names)
	args := []any{string(StatusRunning), now}
	for _, n := range names {
		args = append(args, n)
	}
	rows, err := r.db.SQL().QueryContext(ctx, r.q(`SELECT id, kind, attempt, cancel_requested FROM jobs WHERE status = ? AND lease_until < ?
		AND kind IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")+`)`), args...)
	if err != nil {
		return out, err
	}
	type expired struct {
		id, kind        string
		attempt         int
		cancelRequested bool
	}
	var list []expired
	for rows.Next() {
		var e expired
		var c int64
		if err := rows.Scan(&e.id, &e.kind, &e.attempt, &c); err != nil {
			rows.Close()
			return out, err
		}
		e.cancelRequested = c != 0
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if afterRecoverScan != nil {
		afterRecoverScan()
	}
	for _, e := range list {
		k, ok := r.kind(e.kind)
		if !ok {
			continue // unregistered since the SELECT
		}
		maxAttempts := k.maxAttempts
		var (
			status Status
			msg    string
		)
		switch {
		case e.cancelRequested:
			status, msg = StatusCancelled, "cancelled; its runner stopped before it ended"
		case e.attempt >= maxAttempts:
			status, msg = StatusFailed, fmt.Sprintf("lease expired on attempt %d of %d: its runner stopped or hung", e.attempt, maxAttempts)
		default:
			status, msg = StatusQueued, fmt.Sprintf("lease expired on attempt %d: queued again to resume", e.attempt)
		}
		settle := func(status Status, msg string) (bool, error) {
			res, err := r.db.SQL().ExecContext(ctx, r.q(`UPDATE jobs SET status = ?, error = ?, finished_at = ?, lease_owner = NULL, lease_until = NULL
				WHERE id = ? AND status = ? AND lease_until < ?`), string(status), msg, now, e.id, string(StatusRunning), now)
			if err != nil {
				return false, err
			}
			n, _ := res.RowsAffected()
			return n == 1, nil
		}
		var done bool
		if status == StatusQueued {
			// The requeue only applies while no cancel is requested: a cancel
			// accepted after the SELECT above must not be handed to the next
			// claim as an ordinary queued job.
			var res interface{ RowsAffected() (int64, error) }
			res, err = r.db.SQL().ExecContext(ctx, r.q(`UPDATE jobs SET status = ?, lease_owner = NULL, lease_until = NULL
				WHERE id = ? AND status = ? AND lease_until < ? AND cancel_requested = 0`), string(StatusQueued), e.id, string(StatusRunning), now)
			if err != nil {
				return out, err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				done = true
			} else {
				status, msg = StatusCancelled, "cancelled; its runner stopped before it ended"
				done, err = settle(status, msg)
			}
		} else {
			done, err = settle(status, msg)
		}
		if err != nil {
			return out, err
		}
		if !done {
			continue // renewed or settled meanwhile
		}
		switch status {
		case StatusQueued:
			out.Requeued++
			r.poke()
		case StatusFailed:
			out.Failed++
		case StatusCancelled:
			out.Cancelled++
		}
		r.systemLog(ctx, e.id, "warn", msg)
		r.opts.Logger.Warn("job lease expired", "job", e.id, "kind", e.kind, "outcome", string(status))
	}
	return out, nil
}

// systemLog appends a line outside a running handler (recovery, requeue).
func (r *Runner) systemLog(ctx context.Context, jobID, level, line string) {
	if _, err := r.appendLog(ctx, jobID, r.now(), level, line, int64(r.opts.LogHead), int64(r.opts.LogTail)); err != nil {
		r.opts.Logger.Warn("job log write failed", "job", jobID, "error", err.Error())
	}
}

// run executes one claimed job to its end and records the outcome.
func (r *Runner) run(j *Job) {
	k, ok := r.kind(j.Kind)
	if !ok { // claimed only registered kinds; unregistered since
		r.finish(j, StatusFailed, nil, "kind "+j.Kind+" is not registered on this runner")
		return
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	r.mu.Lock()
	if r.stopped { // claimed while stopping: hand it back untouched
		r.mu.Unlock()
		cancel(ErrShutdown)
		r.unclaim(j)
		return
	}
	r.running[claimKey(j)] = cancel
	r.jobsWG.Add(1)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, claimKey(j))
		r.mu.Unlock()
		cancel(nil)
		r.jobsWG.Done()
	}()

	log := r.newLogger(j)
	prog := &progressBox{}
	if j.Attempt > 1 {
		log.Warnf("resuming: attempt %d", j.Attempt)
	}
	watchDone := make(chan struct{})
	watchStop := make(chan struct{})
	go func() {
		defer close(watchDone)
		r.watch(ctx, cancel, j, prog, watchStop)
	}()

	result, err := r.call(ctx, k.handler, j, log, prog.set)
	log.Flush()

	close(watchStop)
	<-watchDone
	r.flushProgress(j, prog)
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, ErrLeaseLost):
		r.opts.Logger.Warn("job lease lost; its result is not recorded", "job", j.ID)
	case err == nil:
		r.finishResult(j, StatusSucceeded, result, "")
	case errors.Is(err, ErrCancelled) || errors.Is(cause, ErrCancelled):
		log.Warnf("cancelled: %v", err)
		r.finishResult(j, StatusCancelled, result, err.Error())
	case errors.Is(cause, ErrShutdown):
		log.Warnf("runner shutting down: queued again to resume")
		r.requeue(j)
	default:
		log.Errorf("failed: %v", err)
		r.finishResult(j, StatusFailed, result, err.Error())
	}
}

// claimKey identifies one claim of a job: its id and attempt. A lease that
// expired can be reclaimed by another worker of this same runner while the
// old attempt still runs, so the job id alone does not name a claim.
func claimKey(j *Job) string { return fmt.Sprintf("%s#%d", j.ID, j.Attempt) }

// call runs the handler and turns a panic into an error.
func (r *Runner) call(ctx context.Context, h Handler, j *Job, log Logger, progress func(any)) (result any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panicked: %v", p)
			r.opts.Logger.Error("job handler panicked", "job", j.ID, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
	}()
	return h(ctx, j, log, progress)
}

// watch renews the lease, polls for a cancel request and writes progress
// until stop closes.
func (r *Runner) watch(ctx context.Context, cancel context.CancelCauseFunc, j *Job, prog *progressBox, stop <-chan struct{}) {
	poll := time.NewTicker(r.opts.PollEvery)
	defer poll.Stop()
	beat := time.NewTicker(r.opts.HeartbeatEvery)
	defer beat.Stop()
	progT := time.NewTicker(r.opts.ProgressEvery)
	defer progT.Stop()
	bg := context.Background()
	for {
		select {
		case <-stop:
			return
		case <-progT.C:
			r.flushProgress(j, prog)
		case <-poll.C:
			if ctx.Err() != nil {
				continue
			}
			var c int64
			err := r.db.SQL().QueryRowContext(bg, r.q(`SELECT cancel_requested FROM jobs WHERE id = ?`), j.ID).Scan(&c)
			if err == nil && c != 0 {
				cancel(ErrCancelled)
			}
		case <-beat.C:
			// Renewed while the handler runs, also after a cancel or a
			// shutdown request, so the in-flight work can seal in peace.
			res, err := r.db.SQL().ExecContext(bg, r.q(`UPDATE jobs SET lease_until = ? WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = ?`),
				r.now().Add(r.opts.LeaseFor).UnixMilli(), j.ID, r.opts.Owner, j.Attempt, string(StatusRunning))
			if err != nil {
				r.opts.Logger.Warn("job heartbeat failed; retrying", "job", j.ID, "error", err.Error())
				continue
			}
			if n, _ := res.RowsAffected(); n == 0 {
				cancel(ErrLeaseLost)
				return
			}
		}
	}
}

// progressBox holds the latest progress until it is written.
type progressBox struct {
	mu    sync.Mutex
	data  []byte
	dirty bool
}

func (p *progressBox) set(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"error": "progress is not JSON: " + err.Error()})
	}
	p.mu.Lock()
	p.data, p.dirty = b, true
	p.mu.Unlock()
}

func (p *progressBox) take() ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dirty {
		return nil, false
	}
	p.dirty = false
	return p.data, true
}

func (r *Runner) flushProgress(j *Job, prog *progressBox) {
	b, ok := prog.take()
	if !ok {
		return
	}
	if _, err := r.db.SQL().ExecContext(context.Background(), r.q(`UPDATE jobs SET progress = ? WHERE id = ? AND lease_owner = ? AND attempt = ?`),
		string(b), j.ID, r.opts.Owner, j.Attempt); err != nil {
		r.opts.Logger.Warn("job progress write failed", "job", j.ID, "error", err.Error())
	}
}

func (r *Runner) finishResult(j *Job, status Status, result any, msg string) {
	var b []byte
	if result != nil {
		var err error
		if b, err = json.Marshal(result); err != nil {
			b, _ = json.Marshal(map[string]string{"error": "result is not JSON: " + err.Error()})
		}
	}
	r.finish(j, status, b, msg)
}

func (r *Runner) finish(j *Job, status Status, result []byte, msg string) {
	var res any
	if result != nil {
		res = string(result)
	}
	now := r.now()
	out, err := r.db.SQL().ExecContext(context.Background(), r.q(`UPDATE jobs SET status = ?, result = ?, error = ?, finished_at = ?,
		lease_owner = NULL, lease_until = NULL WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = ?`),
		string(status), res, msg, now.UnixMilli(), j.ID, r.opts.Owner, j.Attempt, string(StatusRunning))
	if err != nil {
		r.opts.Logger.Error("job finish write failed", "job", j.ID, "error", err.Error())
		return
	}
	if n, _ := out.RowsAffected(); n == 1 { // web parity L19 finish hooks (finishhook.go)
		runFinishHooks(j, status, result, msg, now)
	}
}

// requeue hands a job this runner holds back to the queue (shutdown); the
// next claim counts an attempt and resumes it.
func (r *Runner) requeue(j *Job) {
	// A job someone already asked to stop is finished as cancelled, not
	// queued again: queued with a pending cancel it showed "Stopping" forever.
	if res, err := r.db.SQL().ExecContext(context.Background(), r.q(`UPDATE jobs SET status = ?, lease_owner = NULL, lease_until = NULL,
		finished_at = ?, error = ? WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = ? AND cancel_requested = 1`),
		string(StatusCancelled), r.now().UnixMilli(), "cancelled while the server restarted", j.ID, r.opts.Owner, j.Attempt, string(StatusRunning)); err == nil {
		if n, _ := res.RowsAffected(); n == 1 {
			return
		}
	}
	if _, err := r.db.SQL().ExecContext(context.Background(), r.q(`UPDATE jobs SET status = ?, lease_owner = NULL, lease_until = NULL
		WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = ?`), string(StatusQueued), j.ID, r.opts.Owner, j.Attempt, string(StatusRunning)); err != nil {
		r.opts.Logger.Error("job requeue failed; its lease expires instead", "job", j.ID, "error", err.Error())
	}
}

// unclaim hands back a job this runner claimed but never started (it began
// stopping meanwhile): queued again with the claim's attempt taken back.
func (r *Runner) unclaim(j *Job) {
	if _, err := r.db.SQL().ExecContext(context.Background(), r.q(`UPDATE jobs SET status = ?, lease_owner = NULL, lease_until = NULL,
		attempt = attempt - 1 WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = ?`), string(StatusQueued), j.ID, r.opts.Owner, j.Attempt, string(StatusRunning)); err != nil {
		r.opts.Logger.Error("job unclaim failed; its lease expires instead", "job", j.ID, "error", err.Error())
	}
}
