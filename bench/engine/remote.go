package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Remote worker limits and file names (docs/remote-execution.md).
const (
	// DefaultMaxUploadBytes caps one uploaded archive (compressed).
	DefaultMaxUploadBytes int64 = 512 << 20
	// DefaultMaxRunBytes caps the extracted run folder of one upload.
	DefaultMaxRunBytes int64 = 2 << 30
	// DefaultChunkBytes is the largest upload body: below the API server's
	// 1 MiB request cap.
	DefaultChunkBytes = 512 << 10
	// maxRejections is how many rejected uploads a worker may send before it
	// gets no further leases.
	maxRejections = 3
	stagingFolder = ".remote"
)

// RemoteOptions configures a Coordinator.
type RemoteOptions struct {
	Token string // the shared worker token; only its sha256 is kept
	// OrgID, when set, makes this a hosted coordinator of one organization
	// (Runners area, docs/runners.md): there is no shared token, every call
	// must carry a schemas.RunnerScope of this organization on its context
	// (the worker routes put it there from the runner token), and a runner
	// leases only experiments whose runner pool is its token's pool. Token
	// must then be empty. Unset, the coordinator is the local one, unchanged.
	OrgID string
	// Book records hosted runners outside the coordinator's memory (the
	// runners table); nil records nothing. Never called in local mode.
	Book RunnerBook
	// StaleAfter marks a worker with no lease that has not called for this
	// long as lost in the list. Leases use the attached scheduler's own
	// stale threshold and heartbeat interval, the same as its local slots.
	StaleAfter     time.Duration
	MaxUploadBytes int64
	MaxRunBytes    int64
	ChunkBytes     int
	// StagingDir holds uploads until they are accepted, and one journal per
	// experiment (<exp id>.jsonl) of every lease, refusal, rejection and
	// loss. Default: <store root>/.remote.
	StagingDir string
	Logger     schemas.Logger
}

// RunnerBook records hosted runners as the coordinator sees them (Runners
// area): on every registration and, as often as it chooses to write, on
// lease and heartbeat calls. It is called outside the coordinator's lock and
// must not block for long; its errors are its own to log.
type RunnerBook interface {
	RunnerSeen(ctx context.Context, scope schemas.RunnerScope, info schemas.WorkerInfo, registered bool)
}

// Coordinator is the server side of remote execution. It lives in the
// process that runs the experiment's Scheduler, which attaches each running
// experiment (RunOptions.Remote): remote workers then lease slots from the
// same queue, hold them under the same store leases, and reserve against the
// same spend cap as the local workers. It keeps the lease table in memory;
// the store's lease files are what make a slot safe against any other
// scheduler.
type Coordinator struct {
	store    schemas.Store
	logger   schemas.Logger
	opts     RemoteOptions
	tokenSum [sha256.Size]byte
	now      func() time.Time

	mu      sync.Mutex
	workers map[string]*remoteWorker
	order   []string    // worker ids in first registration order
	exps    []*schedRun // attached experiments, in attach order
	leases  map[string]*remoteLease
	ended   map[string]string // lease id -> why it ended; a late call for it is refused with this
}

// remoteWorker is one registered worker.
type remoteWorker struct {
	info     schemas.WorkerInfo
	scope    schemas.RunnerScope // hosted: the token it registered with; zero in local mode
	parts    map[string]string
	refused  map[string]string // exp id -> why the worker refused it
	rejected int
}

// remoteLease is one slot out on a remote worker.
type remoteLease struct {
	id, worker, owner string
	r                 *schedRun
	slot              *schemas.Slot
	attempt           int
	est               float64 // the slot's spend reservation
	lastBeat          time.Time
	archive           string // staged upload
	gpus              int    // X7: GPUs the slot's task holds on the worker (gpu.go)
	received          int64
	uploading         bool // a chunk is being written
	closing           bool // complete or release in progress; the sweep leaves it alone
}

// NewCoordinator returns a coordinator over reg's store. The token must be
// set: the worker API never runs without one.
func NewCoordinator(reg *schemas.Registry, opts RemoteOptions) (*Coordinator, error) {
	if reg == nil || reg.Store == nil {
		return nil, errors.New("coordinator: no store registered")
	}
	switch {
	case opts.OrgID != "" && opts.Token != "":
		return nil, errors.New("coordinator: a hosted coordinator takes runner tokens, not a shared worker token")
	case opts.OrgID == "" && strings.TrimSpace(opts.Token) == "":
		return nil, errors.New("coordinator: empty worker token")
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = DefaultStaleAfter
	}
	if opts.MaxUploadBytes <= 0 {
		opts.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if opts.MaxRunBytes <= 0 {
		opts.MaxRunBytes = DefaultMaxRunBytes
	}
	if opts.ChunkBytes <= 0 {
		opts.ChunkBytes = DefaultChunkBytes
	}
	if opts.StagingDir == "" {
		opts.StagingDir = filepath.Join(reg.Store.Root(), stagingFolder)
	}
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if err := os.MkdirAll(opts.StagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("coordinator: staging folder: %w", err)
	}
	c := &Coordinator{store: reg.Store, logger: opts.Logger, opts: opts, now: time.Now,
		workers: map[string]*remoteWorker{}, leases: map[string]*remoteLease{}, ended: map[string]string{}}
	if opts.OrgID == "" {
		c.tokenSum = sha256.Sum256([]byte(opts.Token))
	}
	c.opts.Token = ""
	return c, nil
}

// CheckToken reports whether tok is the worker token. Both sides are hashed
// first, so the comparison is constant time whatever the lengths. A hosted
// coordinator has no shared token and accepts none.
func (c *Coordinator) CheckToken(tok string) bool {
	if c.opts.OrgID != "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	return tok != "" && subtle.ConstantTimeCompare(sum[:], c.tokenSum[:]) == 1
}

// OrgID is the organization of a hosted coordinator, empty for the local
// one. The worker routes authenticate by runner token when it is set.
func (c *Coordinator) OrgID() string { return c.opts.OrgID }

// ChunkBytes is the largest upload chunk the coordinator accepts.
func (c *Coordinator) ChunkBytes() int { return c.opts.ChunkBytes }

// Register records a worker, or refreshes it. A worker that registers again
// while it holds leases restarted, so those leases are lost at once instead
// of waiting for the stale threshold.
func (c *Coordinator) Register(ctx context.Context, req *schemas.WorkerRegister) (*schemas.WorkerRegistered, error) {
	if req == nil || !validWorkerID(req.WorkerID) {
		return nil, fmt.Errorf("%w: worker_id must be 1 to 64 letters, digits, '-', '_' or '.'", schemas.ErrWorkerRequest)
	}
	if req.Slots < 1 || req.Slots > 64 {
		return nil, fmt.Errorf("%w: slots must be 1 to 64", schemas.ErrWorkerRequest)
	}
	if len(req.Host) > 255 || len(req.Version) > 128 {
		return nil, fmt.Errorf("%w: host or version too long", schemas.ErrWorkerRequest)
	}
	if err := validLabels(req.Pool, req.Labels); err != nil {
		return nil, err
	}
	scope, err := c.callScope(ctx)
	if err != nil {
		return nil, err
	}
	pool := req.Pool
	if c.hosted() {
		switch {
		case pool == "":
			pool = scope.Pool
		case pool != scope.Pool:
			return nil, fmt.Errorf("%w: the runner asks for pool %q, its token is scoped to pool %q", schemas.ErrWorkerScope, pool, scope.Pool)
		}
	}
	c.mu.Lock()
	w, ok := c.workers[req.WorkerID]
	if ok && c.hosted() && w.scope.TokenID != scope.TokenID {
		// Same name, another token: not a restart of this runner. Taking the
		// name over would lose the other runner's leases and rebind its
		// scope, so the operator must remove that registration first.
		c.mu.Unlock()
		return nil, fmt.Errorf("worker %s: %w: the name is registered with another runner token; remove that runner first", req.WorkerID, schemas.ErrWorkerScope)
	}
	if !ok {
		w = &remoteWorker{refused: map[string]string{}}
		c.workers[req.WorkerID] = w
		c.order = append(c.order, req.WorkerID)
	}
	for _, l := range c.leases {
		if l.worker == req.WorkerID && !l.closing {
			c.loseLocked(l, "the worker registered again, so the process that held the lease is gone")
		}
	}
	w.info = schemas.WorkerInfo{ID: req.WorkerID, Host: req.Host, Version: req.Version, State: schemas.WorkerIdle,
		Slots: req.Slots, Running: []string{}, Completed: w.info.Completed, LastSeen: c.now().UTC(),
		Pool: pool, Labels: append([]string(nil), req.Labels...), TokenID: scope.TokenID}
	w.parts = copyStrings(req.Parts)
	w.scope = scope
	w.scope.Pool = pool
	if req.Capabilities != nil { // X7: the GPUs the worker offers (gpu.go)
		w.info.GPUs = append([]schemas.GPUInfo(nil), req.Capabilities.GPUs...)
	}
	info, seen := c.infoLocked(w), w.scope
	c.mu.Unlock()
	c.logger.Info("remote worker registered", "worker", req.WorkerID, "host", req.Host, "version", req.Version, "slots", req.Slots, "pool", pool)
	c.book(ctx, seen, info, true)
	return &schemas.WorkerRegistered{WorkerID: req.WorkerID, MaxUploadBytes: c.opts.MaxUploadBytes, ChunkBytes: c.opts.ChunkBytes}, nil
}

// Lease hands the worker its next slot, or nil when there is none for now:
// every slot is done, out, or waiting for the spend cap, or the worker holds
// as many leases as it offers slots. An experiment the worker refused is
// never offered to it again.
func (c *Coordinator) Lease(ctx context.Context, workerID string) (*schemas.WorkerLease, error) {
	scope, err := c.callScope(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	w, err := c.workerLocked(workerID, scope)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	w.info.LastSeen = c.now().UTC()
	if w.info.State == schemas.WorkerLost {
		w.info.State = stateOf(w)
	}
	if c.hosted() {
		seen := w.scope
		defer func() { c.book(ctx, seen, c.infoOf(w), false) }()
	}
	if len(w.info.Running) >= w.info.Slots || w.rejected >= maxRejections {
		c.mu.Unlock()
		return nil, nil
	}
	var exps []*schedRun
	for _, r := range c.exps {
		if _, refused := w.refused[r.m.ExpID]; !refused && c.openTo(w, r) {
			exps = append(exps, r)
		}
	}
	accept := remoteGPUAccept(w.info.GPUs, c.gpusHeldLocked(workerID)) // X7: GPU slots only to a worker with free GPUs (gpu.go)
	c.mu.Unlock()
	for _, r := range exps {
		id := utils.NewID("l", 12)
		owner := "worker:" + workerID + ":" + id
		slot, attempt, est, ok := r.leaseRemoteWhere(owner, accept) // X7
		if !ok {
			continue
		}
		l := &remoteLease{id: id, worker: workerID, owner: owner, r: r, slot: slot, attempt: attempt, est: est,
			lastBeat: c.now(), archive: filepath.Join(c.opts.StagingDir, id+".tar.gz")}
		l.gpus = r.suite.TaskByID(slot.Task).GPUCount() // X7
		c.mu.Lock()
		if !c.attachedLocked(r) {
			c.mu.Unlock()
			r.endRemote(l, false)
			continue
		}
		if l.gpus > 0 && c.gpusHeldLocked(workerID)+l.gpus > len(w.info.GPUs) { // X7: a concurrent lease call took the worker's GPUs
			c.mu.Unlock()
			r.endRemote(l, true)
			return nil, nil
		}
		if len(w.info.Running) >= w.info.Slots {
			// A concurrent lease call of the same worker took its last slot.
			c.mu.Unlock()
			r.endRemote(l, true)
			return nil, nil
		}
		c.leases[id] = l
		w.info.Running = append(w.info.Running, slot.SlotID)
		w.info.State = schemas.WorkerRunning
		c.mu.Unlock()
		c.journal(r, "leased", l, "")
		r.emit(Event{Kind: EventStage, SlotID: slot.SlotID, Stage: schemas.StageLeased,
			Message: fmt.Sprintf("leased to remote worker %s (attempt %d)", workerID, attempt)})
		return c.leaseView(l), nil
	}
	return nil, nil
}

// Manifest returns the manifest of an attached experiment to a registered
// worker, which recomputes its hash and compares it with its own plan.
func (c *Coordinator) Manifest(ctx context.Context, workerID, expID string) (*schemas.Manifest, error) {
	scope, err := c.callScope(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	w, err := c.workerLocked(workerID, scope)
	if err != nil {
		return nil, err
	}
	for _, r := range c.exps {
		if r.m.ExpID == expID && c.openTo(w, r) {
			return r.m, nil
		}
	}
	return nil, fmt.Errorf("experiment %s is not open to workers: %w", expID, schemas.ErrNotFound)
}

// Heartbeat keeps a lease, and the slot's store lease, fresh. When the store
// says the slot is no longer this lease's (another owner reclaimed it, or it
// is gone), the lease is lost and the worker must stop.
func (c *Coordinator) Heartbeat(ctx context.Context, leaseID, workerID string) error {
	scope, err := c.callScope(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	l, err := c.leaseLocked(leaseID, workerID, scope)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	err = c.store.Heartbeat(ctx, l.slot.SlotID, l.owner)
	if errors.Is(err, schemas.ErrLeaseHeld) || errors.Is(err, schemas.ErrNotFound) {
		c.mu.Lock()
		if c.leases[l.id] == l && !l.closing {
			c.loseLocked(l, "the slot's store lease was lost: "+err.Error())
		}
		c.mu.Unlock()
		return fmt.Errorf("lease %s: %w: %v", leaseID, schemas.ErrLeaseGone, err)
	}
	if err != nil {
		return fmt.Errorf("lease %s: heartbeat: %w", leaseID, err)
	}
	c.mu.Lock()
	if c.leases[l.id] == l {
		l.lastBeat = c.now()
	}
	w := c.workers[workerID]
	var seen schemas.RunnerScope
	if w != nil {
		seen = w.scope
	}
	c.mu.Unlock()
	if c.hosted() && w != nil {
		c.book(ctx, seen, c.infoOf(w), false)
	}
	return nil
}

// Upload appends one chunk of the lease's archive. offset must be the number
// of bytes already held, so a retried chunk is refused instead of written
// twice; the archive may not pass MaxUploadBytes.
func (c *Coordinator) Upload(ctx context.Context, leaseID, workerID string, offset int64, data []byte) (int64, error) {
	scope, err := c.callScope(ctx)
	if err != nil {
		return 0, err
	}
	if len(data) > c.opts.ChunkBytes {
		return 0, fmt.Errorf("chunk of %d bytes is larger than %d: %w", len(data), c.opts.ChunkBytes, schemas.ErrUploadTooLarge)
	}
	c.mu.Lock()
	l, err := c.leaseLocked(leaseID, workerID, scope)
	switch {
	case err != nil:
		c.mu.Unlock()
		return 0, err
	case l.closing || l.uploading:
		have := l.received
		c.mu.Unlock()
		return have, fmt.Errorf("lease %s: another upload call is in progress: %w", leaseID, schemas.ErrUploadOffset)
	case offset != l.received:
		have := l.received
		c.mu.Unlock()
		return have, fmt.Errorf("lease %s: offset %d, coordinator holds %d bytes: %w", leaseID, offset, have, schemas.ErrUploadOffset)
	case l.received+int64(len(data)) > c.opts.MaxUploadBytes:
		c.mu.Unlock()
		return offset, fmt.Errorf("lease %s: archive passes %d bytes: %w", leaseID, c.opts.MaxUploadBytes, schemas.ErrUploadTooLarge)
	}
	l.uploading = true
	c.mu.Unlock()
	werr := appendFile(l.archive, data)
	c.mu.Lock()
	defer c.mu.Unlock()
	l.uploading = false
	if werr != nil {
		return l.received, fmt.Errorf("lease %s: stage upload: %w", leaseID, werr)
	}
	l.received += int64(len(data))
	return l.received, nil
}

// Complete decides an uploaded run. The archive is checked against the size
// and sha256 the worker reports, extracted safely, and the worker's seal is
// verified with the store's rules; the run must be the leased slot's and
// attempt's. An accepted run is written into the store, sealed into this
// store's chain and verified. A rejected one is answered with the reason and
// its slot goes back to the queue. A lease that is gone (lost, released,
// already completed, or its experiment stopped) refuses the call, so a late
// upload for a re-leased slot is never counted.
func (c *Coordinator) Complete(ctx context.Context, leaseID string, req *schemas.WorkerComplete) (*schemas.WorkerResult, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: empty body", schemas.ErrWorkerRequest)
	}
	scope, err := c.callScope(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	l, err := c.leaseLocked(leaseID, req.WorkerID, scope)
	if err == nil && (l.closing || l.uploading) {
		err = fmt.Errorf("lease %s: a call is already in progress: %w", leaseID, schemas.ErrLeaseGone)
	}
	if err != nil {
		c.mu.Unlock()
		c.refusedLate(leaseID, req.WorkerID, err)
		return nil, err
	}
	l.closing = true
	c.mu.Unlock()
	res, reason := c.accept(ctx, l, req)
	out := &schemas.WorkerResult{LeaseID: leaseID, RunID: req.RunID, Accepted: reason == ""}
	c.mu.Lock()
	w := c.workers[l.worker]
	c.endLocked(l, "completed")
	if reason != "" {
		out.Reason = reason
		c.ended[l.id] = "rejected: " + reason
		if w != nil {
			w.rejected++
		}
	} else if w != nil {
		w.info.Completed++
	}
	c.mu.Unlock()
	c.remove(l.archive)
	if reason != "" {
		c.logger.Error("remote upload rejected; the slot is released for another run", "experiment", l.r.m.ExpID,
			"slot", l.slot.SlotID, "worker", l.worker, "lease", l.id, "reason", reason)
		l.r.count(func(s *Summary) { s.RemoteRejected++ })
		usd, known := l.r.charge(&req.CostUSD, l.est, "rejected upload of lease "+l.id)
		c.journalCharge(l.r, "rejected", l, reason, usd, known) // before the answer: a resumed scheduler counts it again
		l.r.emit(Event{Kind: EventError, SlotID: l.slot.SlotID, RunID: req.RunID, Message: "remote upload rejected: " + reason})
		l.r.endRemote(l, true)
		return out, nil
	}
	out.SealHash = res.Seal.Hash
	l.r.record(res, l.est)
	l.r.count(func(s *Summary) { s.Remote++ })
	c.journal(l.r, "accepted", l, string(res.Run.Outcome))
	l.r.emit(Event{Kind: EventRunSealed, SlotID: l.slot.SlotID, RunID: res.Run.RunID, Stage: schemas.StageSeal, Outcome: res.Run.Outcome,
		Message: "sealed on remote worker " + l.worker})
	again := res.Run.Outcome == schemas.OutcomeAbortedInfra && l.attempt < maxAttempts
	l.r.endRemote(l, again)
	return out, nil
}

// Release takes a lease back without a run. A refusal (the worker's manifest
// hash, suite lock hash or part versions differ) costs nothing and keeps the
// experiment from being offered to that worker again; a run abandoned after
// its agent started is charged its reported cost, or the estimate when that
// is unknown.
func (c *Coordinator) Release(ctx context.Context, leaseID string, req *schemas.WorkerRelease) error {
	if req == nil {
		return fmt.Errorf("%w: empty body", schemas.ErrWorkerRequest)
	}
	scope, err := c.callScope(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	l, err := c.leaseLocked(leaseID, req.WorkerID, scope)
	if err == nil && l.closing {
		err = fmt.Errorf("lease %s: a call is already in progress: %w", leaseID, schemas.ErrLeaseGone)
	}
	if err != nil {
		c.mu.Unlock()
		return err
	}
	why := "released: " + req.Reason
	if req.Refuse {
		why = "refused: " + req.Reason
		if w := c.workers[l.worker]; w != nil {
			w.refused[l.r.m.ExpID] = req.Reason
		}
	}
	c.endLocked(l, why)
	c.mu.Unlock()
	c.remove(l.archive)
	c.logger.Warn("remote lease given back", "experiment", l.r.m.ExpID, "slot", l.slot.SlotID, "worker", l.worker,
		"refused", req.Refuse, "reason", req.Reason)
	event := map[bool]string{true: "refused", false: "released"}[req.Refuse]
	if req.CostUSD != nil {
		usd, known := l.r.charge(req.CostUSD, l.est, "abandoned lease "+l.id)
		c.journalCharge(l.r, event, l, req.Reason, usd, known) // before the answer: a resumed scheduler counts it again
	} else {
		c.journal(l.r, event, l, req.Reason)
	}
	l.r.endRemote(l, true)
	return nil
}

// Workers lists every registered worker, in first registration order.
func (c *Coordinator) Workers() []schemas.WorkerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]schemas.WorkerInfo, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.infoLocked(c.workers[id]))
	}
	return out
}

// Sweep loses every lease whose worker sent no heartbeat for longer than the
// stale threshold, and marks workers silent that long as lost. By then the
// slot's store lease is stale too, so whoever leases the slot next reclaims
// it under the existing lease rules. The attached experiments run it on a
// ticker; it is exported for tests.
func (c *Coordinator) Sweep(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.leases {
		if stale := l.r.opts.StaleAfter; !l.closing && now.Sub(l.lastBeat) > stale {
			c.loseLocked(l, fmt.Sprintf("no heartbeat for %s, past the stale threshold %s", now.Sub(l.lastBeat).Round(time.Millisecond), stale))
		}
	}
	for _, w := range c.workers {
		if now.Sub(w.info.LastSeen) > c.opts.StaleAfter && len(w.info.Running) == 0 {
			w.info.State = schemas.WorkerLost
		}
	}
}

// attach opens r to remote workers until the returned function is called,
// after the local workers of Run have stopped. Any lease still out then (Run
// ended on its context) is ended: its store lease released, its slot counted
// pending, and a later upload for it refused.
func (c *Coordinator) attach(r *schedRun) func() {
	c.mu.Lock()
	c.exps = append(c.exps, r)
	c.mu.Unlock()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(max(min(r.opts.HeartbeatEvery, r.opts.StaleAfter/4), 10*time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				c.Sweep(c.now())
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		c.mu.Lock()
		for i, x := range c.exps {
			if x == r {
				c.exps = append(c.exps[:i:i], c.exps[i+1:]...)
				break
			}
		}
		var left []*remoteLease
		for _, l := range c.leases {
			if l.r == r {
				left = append(left, l)
				c.endLocked(l, "the experiment's scheduler stopped")
			}
		}
		c.mu.Unlock()
		for _, l := range left {
			c.remove(l.archive)
			c.journal(r, "stopped", l, "the experiment's scheduler stopped")
			r.count(func(s *Summary) { s.Pending++ })
			r.endRemote(l, false)
		}
	}
}

// accept verifies and stores an uploaded run. It returns the sealed result,
// or the reason the upload is rejected.
func (c *Coordinator) accept(ctx context.Context, l *remoteLease, req *schemas.WorkerComplete) (*SlotResult, string) {
	if req.ArchiveBytes != l.received {
		return nil, fmt.Sprintf("archive is %d bytes, the coordinator received %d", req.ArchiveBytes, l.received)
	}
	sum, n, err := sha256File(l.archive)
	if err != nil {
		return nil, "read staged archive: " + err.Error()
	}
	if sum != req.ArchiveSHA256 || n != req.ArchiveBytes {
		return nil, fmt.Sprintf("archive hashes to %s, the worker reported %s", sum, req.ArchiveSHA256)
	}
	dir := filepath.Join(c.opts.StagingDir, l.id)
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			c.logger.Warn("remove staged run folder", "path", dir, "error", err.Error())
		}
	}()
	f, err := os.Open(l.archive)
	if err != nil {
		return nil, "open staged archive: " + err.Error()
	}
	files, err := extractRun(f, dir, c.opts.MaxRunBytes)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return nil, err.Error()
	}
	m := l.r.m
	up, err := verifyUpload(dir, files, uploadWant{expID: m.ExpID, slot: l.slot, attempt: l.attempt, manifestHash: m.Hash,
		suiteLockHash: m.SuiteLockHash, runID: req.RunID, sealSHA256: req.SealSHA256})
	if err != nil {
		return nil, "worker seal: " + err.Error()
	}
	if reason := c.checkSlot(ctx, l); reason != "" {
		return nil, reason
	}
	res, err := c.storeRun(ctx, l, req, up)
	if err != nil {
		return nil, err.Error()
	}
	return res, ""
}

// checkSlot confirms, right before the run is written, that the lease still
// owns the slot in the store (refreshing it) and that the slot has no
// counted run, so one slot never gets two.
func (c *Coordinator) checkSlot(ctx context.Context, l *remoteLease) string {
	if err := c.store.Heartbeat(ctx, l.slot.SlotID, l.owner); err != nil {
		return "the slot's store lease is no longer this lease's: " + err.Error()
	}
	recs, err := slotRuns(ctx, c.store, l.r.m.ExpID, l.slot.SlotID)
	if err != nil {
		return err.Error()
	}
	for _, rec := range recs {
		if !rec.Sealed {
			return fmt.Sprintf("the slot has an unsealed run %s; it is settled before another run is accepted", rec.Run.RunID)
		}
		if rec.Run.Outcome != schemas.OutcomeAbortedInfra {
			return fmt.Sprintf("the slot already has a counted run %s", rec.Run.RunID)
		}
	}
	return ""
}

// storeRun writes a verified upload into the store as a new run of the slot,
// adds remote/worker.json and the worker's seal byte for byte as
// remote/seal.json, seals the folder into this store's chain and verifies
// the seal. run.json goes last, so a crash halfway leaves an interrupted run
// that the next attempt settles like any other.
func (c *Coordinator) storeRun(ctx context.Context, l *remoteLease, req *schemas.WorkerComplete, up *sealedUpload) (*SlotResult, error) {
	run := up.run
	first := *run
	dir, err := c.store.NewRun(ctx, l.slot, &first)
	if err != nil {
		return nil, fmt.Errorf("create run folder: %w", err)
	}
	names := make([]string, 0, len(up.files))
	for name := range up.files {
		if name != runJSONFile && name != transitionsFile {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(up.dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if err := c.store.WriteRunFile(ctx, run, name, b); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	if err := c.writeProvenance(ctx, l, req, up); err != nil {
		return nil, err
	}
	for _, t := range up.transitions {
		if err := c.store.AppendTransition(ctx, run, t); err != nil {
			return nil, fmt.Errorf("append transition: %w", err)
		}
	}
	if err := c.store.WriteRunFile(ctx, run, runJSONFile, up.runRaw); err != nil {
		return nil, fmt.Errorf("write run.json: %w", err)
	}
	rec, err := c.store.Seal(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	if err := c.store.Verify(ctx, run.ExpID, run.RunID); err != nil {
		c.logger.Error("accepted remote run does not verify after sealing", "run", run.RunID, "error", err.Error())
	}
	return &SlotResult{Run: run, Dir: dir, Metrics: up.metrics, Seal: rec}, nil
}

// remoteRecord is remote/worker.json in an accepted run.
type remoteRecord struct {
	WorkerID     string        `json:"worker_id"`
	Host         string        `json:"host"`
	Version      string        `json:"version"`
	LeaseID      string        `json:"lease_id"`
	Attempt      int           `json:"attempt"`
	WorkerSeal   string        `json:"worker_seal_sha256"` // sha256 of remote/seal.json
	ArchiveSHA   string        `json:"archive_sha256"`
	ReportedCost schemas.Value `json:"reported_cost_usd"`
	AcceptedAt   time.Time     `json:"accepted_at"`
	// Gateway is the worker's own gateway preflight for the run, reported
	// with the upload when the manifest defers the preflight to runners
	// (Tenancy area); the gateway_hygiene instrument reads it.
	Gateway *schemas.GatewayManifest `json:"gateway,omitempty"`
	// Images are the worker's own image ids for the run, for a deferred
	// manifest planned without Docker (Deploy area); the runner_images
	// instrument reads them.
	Images *schemas.RunnerImages `json:"images,omitempty"`
}

func (c *Coordinator) writeProvenance(ctx context.Context, l *remoteLease, req *schemas.WorkerComplete, up *sealedUpload) error {
	rec := remoteRecord{WorkerID: l.worker, LeaseID: l.id, Attempt: l.attempt, WorkerSeal: up.seal.Hash,
		ArchiveSHA: req.ArchiveSHA256, ReportedCost: req.CostUSD, AcceptedAt: c.now().UTC()}
	if l.r != nil && l.r.m != nil && l.r.m.Gateway.Deferred() { // Tenancy area: only a deferred manifest takes the runner's record
		rec.Gateway = req.Gateway
		rec.Images = req.Images // Deploy area: the runner's image ids (remoteimages.go)
	}
	c.mu.Lock()
	if w := c.workers[l.worker]; w != nil {
		rec.Host, rec.Version = w.info.Host, w.info.Version
	}
	c.mu.Unlock()
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s/worker.json: %w", remoteFolder, err)
	}
	if err := c.store.WriteRunFile(ctx, up.run, remoteFolder+"/worker.json", b); err != nil {
		return fmt.Errorf("write %s/worker.json: %w", remoteFolder, err)
	}
	if err := c.store.WriteRunFile(ctx, up.run, remoteFolder+"/"+sealJSONFile, up.sealRaw); err != nil {
		return fmt.Errorf("write %s/seal.json: %w", remoteFolder, err)
	}
	return nil
}

// leaseLocked returns the worker's live lease, or ErrLeaseGone with the
// reason the lease ended. The caller holds mu.
func (c *Coordinator) leaseLocked(leaseID, workerID string, scope schemas.RunnerScope) (*remoteLease, error) {
	if _, err := c.workerLocked(workerID, scope); err != nil {
		return nil, err
	}
	l, ok := c.leases[leaseID]
	if !ok {
		why := c.ended[leaseID]
		if why == "" {
			why = "no such lease"
		}
		return nil, fmt.Errorf("lease %s: %w: %s", leaseID, schemas.ErrLeaseGone, why)
	}
	if l.worker != workerID {
		return nil, fmt.Errorf("lease %s belongs to another worker: %w", leaseID, schemas.ErrLeaseGone)
	}
	c.workers[workerID].info.LastSeen = c.now().UTC()
	return l, nil
}

// loseLocked ends a lease whose worker went silent or whose store lease is
// gone, under the lease-loss rules: the slot is counted in lease_lost, the
// run the worker may have started is charged at the estimate (its cost can
// never be observed here), and the slot goes back to the queue. The store
// lease is not released; it is stale or another owner's. The caller holds mu.
func (c *Coordinator) loseLocked(l *remoteLease, why string) {
	c.endLocked(l, "lost: "+why)
	if w := c.workers[l.worker]; w != nil && len(w.info.Running) == 0 {
		w.info.State = schemas.WorkerLost
	}
	c.logger.Error("remote lease lost: the slot goes back to the queue, and a late upload for it is refused",
		"experiment", l.r.m.ExpID, "slot", l.slot.SlotID, "worker", l.worker, "lease", l.id, "why", why)
	c.remove(l.archive)
	r := l.r
	r.count(func(s *Summary) { s.LeaseLost++ })
	usd, known := r.charge(nil, l.est, "lost lease "+l.id)
	r.emit(Event{Kind: EventError, SlotID: l.slot.SlotID, Message: "remote lease lost: " + why})
	c.journalLocked(r, journalEntry{Event: "lost", Detail: why, ChargedUSD: &usd, CostKnown: known}, l)
	r.release(l.est)
	r.queue.giveBack(l.slot)
}

// endLocked removes a lease from the table and the worker's running list.
// The caller holds mu.
func (c *Coordinator) endLocked(l *remoteLease, why string) {
	delete(c.leases, l.id)
	c.ended[l.id] = why
	w := c.workers[l.worker]
	if w == nil {
		return
	}
	for i, s := range w.info.Running {
		if s == l.slot.SlotID {
			w.info.Running = append(w.info.Running[:i:i], w.info.Running[i+1:]...)
			break
		}
	}
	w.info.State = stateOf(w)
}

func (c *Coordinator) attachedLocked(r *schedRun) bool {
	for _, x := range c.exps {
		if x == r {
			return true
		}
	}
	return false
}

func (c *Coordinator) leaseView(l *remoteLease) *schemas.WorkerLease {
	m := l.r.m
	return &schemas.WorkerLease{LeaseID: l.id, WorkerID: l.worker, ExpID: m.ExpID, Slot: *l.slot, Attempt: l.attempt,
		ManifestHash: m.Hash, SuiteLockHash: m.SuiteLockHash, Parts: copyStrings(m.Parts), BenchVersion: m.BenchVersion,
		HeartbeatEveryMS: l.r.opts.HeartbeatEvery.Milliseconds(), StaleAfterMS: l.r.opts.StaleAfter.Milliseconds()}
}

// refusedLate records a call for a lease that is gone, most often an upload
// that arrived after the lease was lost and the slot re-leased.
func (c *Coordinator) refusedLate(leaseID, workerID string, err error) {
	if !errors.Is(err, schemas.ErrLeaseGone) {
		return
	}
	c.logger.Warn("late remote upload refused: the lease is gone, so the run is not counted", "lease", leaseID,
		"worker", workerID, "error", err.Error())
}

// journalEntry is one line of <staging>/<exp id>.jsonl.
type journalEntry struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"` // leased | accepted | rejected | refused | released | lost | stopped
	Lease   string    `json:"lease"`
	Worker  string    `json:"worker"`
	Slot    string    `json:"slot"`
	Attempt int       `json:"attempt"`
	Detail  string    `json:"detail,omitempty"`
	// ChargedUSD is what the scheduler counted against the spend cap for a
	// run that was never sealed here (rejected, released after its agent
	// started, lost); CostKnown says it was the observed cost rather than the
	// estimate. A resumed scheduler adds these back (remoteCharges), once per
	// lease.
	ChargedUSD *float64 `json:"charged_usd,omitempty"`
	CostKnown  bool     `json:"cost_known,omitempty"`
}

func (c *Coordinator) journal(r *schedRun, event string, l *remoteLease, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.journalLocked(r, journalEntry{Event: event, Detail: detail}, l)
}

// journalCharge journals an event that charged an unsealed run's spend.
func (c *Coordinator) journalCharge(r *schedRun, event string, l *remoteLease, detail string, usd float64, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.journalLocked(r, journalEntry{Event: event, Detail: detail, ChargedUSD: &usd, CostKnown: known}, l)
}

// journalLocked appends one journal line (e's event, detail and charge, the
// rest from l); a failure is logged, never fatal.
func (c *Coordinator) journalLocked(r *schedRun, e journalEntry, l *remoteLease) {
	e.At, e.Lease, e.Worker, e.Slot, e.Attempt = c.now().UTC(), l.id, l.worker, l.slot.SlotID, l.attempt
	b, err := json.Marshal(e)
	if err == nil {
		err = appendFile(filepath.Join(c.opts.StagingDir, r.m.ExpID+".jsonl"), append(b, '\n'))
	}
	if err != nil {
		c.logger.Warn("remote journal write failed", "experiment", r.m.ExpID, "error", err.Error())
	}
}

// leaseRemote takes the next slot for a remote worker the way process does
// for a local one: a slot with a counted run is skipped, a reservation is
// made, the slot's store lease is taken for owner, and earlier runs are
// settled. ok is false when there is nothing to hand out now: the queue is
// empty, the cap is waiting on in-flight slots, or Run's context ended.
func (r *schedRun) leaseRemote(owner string) (*schemas.Slot, int, float64, bool) {
	return r.leaseRemoteWhere(owner, nil)
}

// leaseRemoteWhere is leaseRemote for the slots whose task accept takes (nil
// takes every slot; X7: the worker's GPUs decide, gpu.go).
func (r *schedRun) leaseRemoteWhere(owner string, accept func(*schemas.Task) bool) (*schemas.Slot, int, float64, bool) {
	store := r.s.reg.Store
	var match func(*schemas.Slot) bool
	if accept != nil {
		match = func(s *schemas.Slot) bool { return accept(r.suite.TaskByID(s.Task)) }
	}
	for r.ctx.Err() == nil {
		slot, ok := r.queue.takeWhere(match)
		if !ok {
			return nil, 0, 0, false
		}
		recs, err := slotRuns(r.ctx, store, r.m.ExpID, slot.SlotID)
		if err != nil {
			r.slotError(slot, err)
			r.queue.finish()
			continue
		}
		if r.skipDone(slot, recs) {
			r.queue.finish()
			continue
		}
		est, ok, wait := r.tryReserve()
		if wait {
			r.queue.giveBack(slot)
			return nil, 0, 0, false
		}
		if !ok {
			r.count(func(s *Summary) { s.Pending++ })
			r.queue.finish()
			continue
		}
		held, err := store.Lease(r.ctx, slot.SlotID, owner, r.opts.StaleAfter)
		if err != nil || !held {
			r.release(est)
			r.queue.finish()
			if err != nil {
				r.slotError(slot, fmt.Errorf("lease: %w", err))
			} else {
				r.count(func(s *Summary) { s.LeaseHeld++ })
				r.emit(Event{Kind: EventLeaseHeld, SlotID: slot.SlotID})
			}
			continue
		}
		attempts, done := r.settle(r.ctx, slot, est)
		if !done && r.capReached() { // recovery charges are in: no new attempt past the cap
			r.count(func(s *Summary) { s.Pending++ })
			done = true
		}
		if done {
			r.releaseStoreLease(slot, owner)
			r.release(est)
			r.queue.finish()
			continue
		}
		if attempts > 0 {
			r.count(func(s *Summary) { s.Retried++ })
		}
		return slot, attempts + 1, est, true
	}
	return nil, 0, 0, false
}

// endRemote finishes a remote lease on the scheduler's side: the store lease
// is released, the reservation returned, and the slot either queued again
// (again) or finished.
func (r *schedRun) endRemote(l *remoteLease, again bool) {
	r.releaseStoreLease(l.slot, l.owner)
	r.release(l.est)
	if again {
		r.queue.giveBack(l.slot)
		return
	}
	r.queue.finish()
}

func (r *schedRun) releaseStoreLease(slot *schemas.Slot, owner string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), cleanupTimeout)
	defer cancel()
	if err := r.s.reg.Store.Release(ctx, slot.SlotID, owner); err != nil {
		r.s.logger.Warn("release remote lease", "slot", slot.SlotID, "owner", owner, "error", err.Error())
	}
}

func stateOf(w *remoteWorker) string {
	if len(w.info.Running) > 0 {
		return schemas.WorkerRunning
	}
	return schemas.WorkerIdle
}

// validWorkerID accepts 1 to 64 letters, digits, '-', '_' and '.', not a dot
// segment: worker ids end up in lease owners and the journal.
func validWorkerID(id string) bool {
	if id == "" || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

func copyStrings(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func appendFile(p string, data []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// remove deletes a staged file; a file that was never written is fine.
func (c *Coordinator) remove(p string) {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		c.logger.Warn("remove staged upload", "path", p, "error", err.Error())
	}
}

// remoteCharges reads the remote journal of r's experiment for the spend
// charged to runs that were never sealed here, one entry per lease, so a
// resumed scheduler counts that spend again: sealed runs alone would reset
// it. The journal is under the attached coordinator's staging folder, else
// the default one under the store root. No journal means no charges; a line
// that does not parse is skipped with a warning.
func (r *schedRun) remoteCharges() ([]journalEntry, error) {
	dir := filepath.Join(r.s.reg.Store.Root(), stagingFolder)
	if r.opts.Remote != nil && r.opts.Remote.opts.StagingDir != "" {
		dir = r.opts.Remote.opts.StagingDir
	}
	b, err := os.ReadFile(filepath.Join(dir, r.m.ExpID+".jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read remote journal: %w", err)
	}
	var out []journalEntry
	seen := map[string]bool{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e journalEntry
		if err := json.Unmarshal(line, &e); err != nil {
			r.s.logger.Warn("unreadable remote journal line", "experiment", r.m.ExpID, "error", err.Error())
			continue
		}
		if e.ChargedUSD == nil || *e.ChargedUSD < 0 || seen[e.Lease] {
			continue
		}
		seen[e.Lease] = true
		out = append(out, e)
	}
	return out, nil
}
