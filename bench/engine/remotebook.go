package engine

// Durable leases (Z11, INFRA_I4 item 3b). With RemoteOptions.Leases set,
// every slot lease handed to a remote worker is also written to a lease
// book (Postgres in hosted mode, one JSON file per lease in local mode) in
// the same step as its journal line. When the server stops for a restart
// (the job runner shuts down) the leases out are kept instead of ended:
// their store lease stays held, nothing is charged, and the book row stays
// open. On boot the coordinator loads the open rows: the workers holding
// them are known again at once, their heartbeats are accepted for a grace
// period, and when the experiment's resumed scheduler attaches, each lease
// is re-attached to its slot, so the run in flight finishes and is counted
// once. A re-attached lease is never charged "unsettled" (remoteCharges).
//
// One slot, one holder still rests on the store's lease.json owner check:
// a lease is re-attached only when its owner still holds the slot's store
// lease and the experiment's journal does not show it ended.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ErrReattaching answers an upload, complete or release of a lease the
// server kept across a restart but has not re-attached yet (its
// experiment's scheduler is still starting). The worker retries.
var ErrReattaching = schemas.ErrLeaseReattaching

// LeaseRecord is one open slot lease in a lease book.
type LeaseRecord struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id"`
	ExpID       string    `json:"exp_id"`
	SlotID      string    `json:"slot_id"`
	WorkerID    string    `json:"worker_id"`
	Owner       string    `json:"owner"` // the store lease owner (worker:<id>:<lease>)
	Attempt     int       `json:"attempt"`
	ReservedUSD float64   `json:"reserved_usd"`
	GPUs        int       `json:"gpus,omitempty"`
	GPUIDs      []string  `json:"gpu_ids,omitempty"`
	LastBeat    time.Time `json:"last_beat"`
	// The worker's registration, so it is known again after a restart
	// without registering (registering again would lose its leases).
	Worker schemas.WorkerInfo  `json:"worker"`
	Parts  map[string]string   `json:"parts,omitempty"`
	Scope  schemas.RunnerScope `json:"scope"`
}

// LeaseBook keeps open leases outside the coordinator's memory.
type LeaseBook interface {
	// PutLease records a new open lease.
	PutLease(ctx context.Context, rec LeaseRecord) error
	// TouchLease moves an open lease's last heartbeat; an ended or unknown
	// lease is left alone.
	TouchLease(ctx context.Context, id string, at time.Time) error
	// EndLease closes a lease (why: completed, lost, released ...).
	EndLease(ctx context.Context, id, why string) error
	// OpenLeases lists the open leases of an organization ("" in local mode).
	OpenLeases(ctx context.Context, orgID string) ([]LeaseRecord, error)
}

// FileLeaseBook is the local mode lease book: one <lease id>.json per open
// lease in Dir, written atomically and synced.
type FileLeaseBook struct{ Dir string }

func (b FileLeaseBook) path(id string) (string, error) {
	if !strings.HasPrefix(id, "l_") || strings.ContainsAny(id, `/\.`) {
		return "", fmt.Errorf("lease book: bad lease id %q", id)
	}
	return filepath.Join(b.Dir, id+".json"), nil
}

func (b FileLeaseBook) write(p string, rec LeaseRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.Dir, ".lease-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(raw)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), p)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

// PutLease implements LeaseBook.
func (b FileLeaseBook) PutLease(_ context.Context, rec LeaseRecord) error {
	p, err := b.path(rec.ID)
	if err != nil {
		return err
	}
	return b.write(p, rec)
}

// TouchLease implements LeaseBook.
func (b FileLeaseBook) TouchLease(_ context.Context, id string, at time.Time) error {
	p, err := b.path(id)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var rec LeaseRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return err
	}
	rec.LastBeat = at.UTC()
	return b.write(p, rec)
}

// EndLease implements LeaseBook.
func (b FileLeaseBook) EndLease(_ context.Context, id, _ string) error {
	p, err := b.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// OpenLeases implements LeaseBook. An unreadable file is skipped: that lease
// is then not re-attached, and the journal charges its reservation.
func (b FileLeaseBook) OpenLeases(_ context.Context, orgID string) ([]LeaseRecord, error) {
	entries, err := os.ReadDir(b.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []LeaseRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "l_") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(b.Dir, e.Name()))
		if err != nil {
			continue
		}
		var rec LeaseRecord
		if json.Unmarshal(raw, &rec) != nil || rec.ID == "" || rec.OrgID != orgID {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// pendingLease is a lease loaded from the book, waiting for its
// experiment's scheduler to attach.
type pendingLease struct {
	rec      LeaseRecord
	lastBeat time.Time
	expired  bool // the grace period passed, or its store lease is gone: lost when attached
}

// bookOp is one asynchronous lease book write; done (tests) is closed once
// every earlier write is done.
type bookOp struct {
	id   string
	at   time.Time
	end  bool
	why  string
	done chan struct{}
}

// touchEvery is how often a lease's heartbeat is written to the book.
func (c *Coordinator) touchEvery() time.Duration { return max(c.opts.StaleAfter/4, time.Second) }

// reattachGrace is how long after boot kept leases' heartbeats are accepted
// before their experiment attaches.
func (c *Coordinator) reattachGrace() time.Duration {
	if c.opts.ReattachGrace > 0 {
		return c.opts.ReattachGrace
	}
	return c.opts.StaleAfter
}

// loadLeases reads the book's open leases at boot. A lease whose last
// heartbeat is older than the stale threshold (plus the book's write lag)
// was given up by its worker: it is closed here and the journal charges it.
func (c *Coordinator) loadLeases() {
	if c.opts.Leases == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	recs, err := c.opts.Leases.OpenLeases(ctx, c.opts.OrgID)
	if err != nil {
		c.logger.Warn("lease book unreadable: leases from before the restart are not re-attached", "error", err.Error())
		return
	}
	now := c.now()
	c.bootAt = now
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	for _, rec := range recs {
		if age := now.Sub(rec.LastBeat); age > c.opts.StaleAfter+c.touchEvery() {
			c.logger.Warn("kept lease too old to re-attach: its worker has given it up", "lease", rec.ID, "experiment", rec.ExpID,
				"slot", rec.SlotID, "age", age.Round(time.Second).String())
			c.bookSend(bookOp{id: rec.ID, end: true, why: "expired before the restart finished"})
			continue
		}
		c.pending[rec.ID] = &pendingLease{rec: rec, lastBeat: now}
		w, ok := c.workers[rec.WorkerID]
		if !ok {
			w = &remoteWorker{refused: map[string]*leaseRefusal{}, info: rec.Worker, parts: copyStrings(rec.Parts), scope: rec.Scope}
			w.info.Running = nil
			w.info.Rejected = 0
			c.workers[rec.WorkerID] = w
			c.order = append(c.order, rec.WorkerID)
		}
		w.info.Running = append(w.info.Running, rec.SlotID)
		w.info.State = schemas.WorkerRunning
		w.info.LastSeen = now.UTC()
		c.logger.Info("kept lease loaded: it is re-attached when its experiment resumes", "lease", rec.ID,
			"experiment", rec.ExpID, "slot", rec.SlotID, "worker", rec.WorkerID)
	}
}

// bookSend queues a book write; a lease book with no room drops a touch
// (the next one carries the time) and sends an end from its own goroutine.
func (c *Coordinator) bookSend(op bookOp) {
	if c.opts.Leases == nil {
		if op.done != nil {
			close(op.done)
		}
		return
	}
	c.bookOnce.Do(func() {
		c.bookQ = make(chan bookOp, 4096)
		go c.bookLoop()
	})
	select {
	case c.bookQ <- op:
	default:
		if op.end || op.done != nil {
			go func() { c.bookQ <- op }()
		}
	}
}

func (c *Coordinator) bookLoop() {
	for op := range c.bookQ {
		if op.done != nil {
			close(op.done)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var err error
		if op.end {
			err = c.opts.Leases.EndLease(ctx, op.id, op.why)
		} else {
			err = c.opts.Leases.TouchLease(ctx, op.id, op.at)
		}
		cancel()
		if err != nil {
			c.logger.Warn("lease book write failed", "lease", op.id, "end", op.end, "error", err.Error())
		}
	}
}

// flushBook waits until every queued book write is done (tests).
func (c *Coordinator) flushBook() {
	done := make(chan struct{})
	c.bookSend(bookOp{done: done})
	<-done
}

// bookPut records a new lease; a failure only costs its re-attachment
// after a restart, so the lease is still handed out.
func (c *Coordinator) bookPut(l *remoteLease) {
	if c.opts.Leases == nil {
		return
	}
	c.mu.Lock()
	rec := c.recordLocked(l)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.opts.Leases.PutLease(ctx, rec); err != nil {
		c.logger.Warn("lease not written to the lease book: a server restart stops its run", "lease", l.id, "error", err.Error())
	}
}

// isPending reports whether lease id of expID was kept across a restart and
// waits to be re-attached: remoteCharges must not charge it.
func (c *Coordinator) isPending(expID, id string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[id]
	return ok && p.rec.ExpID == expID
}

// expirePendingLocked marks a kept lease lost-on-attach and frees its slot
// on the worker. The caller holds mu.
func (c *Coordinator) expirePendingLocked(p *pendingLease, why string) {
	if p.expired {
		return
	}
	p.expired = true
	if w := c.workers[p.rec.WorkerID]; w != nil {
		for i, s := range w.info.Running {
			if s == p.rec.SlotID {
				w.info.Running = append(w.info.Running[:i:i], w.info.Running[i+1:]...)
				break
			}
		}
		w.info.State = stateOf(w)
	}
	c.logger.Warn("kept lease given up", "lease", p.rec.ID, "experiment", p.rec.ExpID, "slot", p.rec.SlotID, "why", why)
}

// pendingCall answers an upload, complete or release of a kept lease that
// is not re-attached yet: handled is false when leaseID is not one.
func (c *Coordinator) pendingCall(leaseID, workerID string, scope schemas.RunnerScope) (handled bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[leaseID]
	if !ok {
		return false, nil
	}
	if _, err := c.workerLocked(workerID, scope); err != nil {
		return true, err
	}
	if p.rec.WorkerID != workerID {
		return true, fmt.Errorf("lease %s belongs to another worker: %w", leaseID, schemas.ErrLeaseGone)
	}
	if p.expired {
		return true, fmt.Errorf("lease %s: %w: the server restarted and the experiment did not resume in time", leaseID, schemas.ErrLeaseGone)
	}
	return true, fmt.Errorf("lease %s: %w", leaseID, ErrReattaching)
}

// pendingHeartbeat keeps a kept lease and its store lease fresh until its
// experiment attaches, for the grace period after boot.
func (c *Coordinator) pendingHeartbeat(ctx context.Context, leaseID, workerID string, scope schemas.RunnerScope) (bool, error) {
	c.mu.Lock()
	p, ok := c.pending[leaseID]
	if !ok {
		c.mu.Unlock()
		return false, nil
	}
	w, err := c.workerLocked(workerID, scope)
	switch {
	case err != nil:
	case p.rec.WorkerID != workerID:
		err = fmt.Errorf("lease %s belongs to another worker: %w", leaseID, schemas.ErrLeaseGone)
	case !p.expired && c.now().Sub(c.bootAt) > c.reattachGrace():
		c.expirePendingLocked(p, fmt.Sprintf("its experiment did not resume within %s of the restart", c.reattachGrace()))
		fallthrough
	case p.expired:
		err = fmt.Errorf("lease %s: %w: the server restarted and the experiment did not resume in time", leaseID, schemas.ErrLeaseGone)
	default:
		w.info.LastSeen = c.now().UTC()
	}
	rec := p.rec
	c.mu.Unlock()
	if err != nil {
		return true, err
	}
	if err := c.store.Heartbeat(ctx, rec.SlotID, rec.Owner); err != nil {
		if errors.Is(err, schemas.ErrLeaseHeld) || errors.Is(err, schemas.ErrNotFound) {
			c.mu.Lock()
			c.expirePendingLocked(p, "the slot's store lease was lost: "+err.Error())
			c.mu.Unlock()
			return true, fmt.Errorf("lease %s: %w: %v", leaseID, schemas.ErrLeaseGone, err)
		}
		return true, fmt.Errorf("lease %s: heartbeat: %w", leaseID, err)
	}
	now := c.now()
	c.mu.Lock()
	p.lastBeat = now
	c.mu.Unlock()
	c.bookSend(bookOp{id: leaseID, at: now})
	return true, nil
}

// dropPendingOfLocked gives up the kept leases of a worker that registered
// again: its process restarted, so their runs are gone. The caller holds mu.
func (c *Coordinator) dropPendingOfLocked(workerID string) {
	for _, p := range c.pending {
		if p.rec.WorkerID == workerID {
			c.expirePendingLocked(p, "the worker registered again, so the process that held the lease is gone")
		}
	}
}

// revive re-attaches the kept leases of r's experiment before r is opened
// to workers, so no other lease call can take their slots first.
func (c *Coordinator) revive(r *schedRun) {
	c.mu.Lock()
	var mine []*pendingLease
	for _, p := range c.pending {
		if p.rec.ExpID == r.m.ExpID {
			// Left in pending until it is in the lease table, so a heartbeat
			// racing the re-attach is still answered (reviveOne).
			mine = append(mine, p)
		}
	}
	c.mu.Unlock()
	if len(mine) == 0 {
		return
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].rec.ID < mine[j].rec.ID })
	ended := r.journalEndedLeases()
	for _, p := range mine {
		c.reviveOne(r, p, ended[p.rec.ID])
	}
}

func (c *Coordinator) reviveOne(r *schedRun, p *pendingLease, ended bool) {
	rec := p.rec
	if ended { // the book's end did not reach it before the stop; the journal is the record
		c.mu.Lock()
		c.expirePendingLocked(p, "the journal shows the lease ended")
		delete(c.pending, rec.ID)
		c.ended[rec.ID] = "ended before the restart"
		c.mu.Unlock()
		c.bookSend(bookOp{id: rec.ID, end: true, why: "ended before the restart"})
		return
	}
	l := &remoteLease{id: rec.ID, worker: rec.WorkerID, owner: rec.Owner, r: r, attempt: rec.Attempt, est: rec.ReservedUSD,
		lastBeat: p.lastBeat, persisted: p.lastBeat, started: c.now(), // Y16: metered from the re-attach (the book keeps no start time)
		archive: filepath.Join(c.opts.StagingDir, rec.ID+".tar.gz"),
		gpus:    rec.GPUs, gpuIDs: append([]string(nil), rec.GPUIDs...)}
	slot, queued := r.queue.takeByID(rec.SlotID)
	if !queued {
		// The slot is done, held out by an operator, or not part of this
		// run: the worker's run is not counted, and its spend is charged
		// at the reservation like a lost lease, with nothing queued.
		l.slot = &schemas.Slot{SlotID: rec.SlotID}
		c.mu.Lock()
		c.expirePendingLocked(p, "its slot is not waiting in the resumed experiment")
		delete(c.pending, rec.ID)
		c.ended[l.id] = "lost: the slot is not waiting in the resumed experiment"
		usd, known := r.charge(nil, l.est, "kept lease "+l.id)
		if err := c.journalLocked(r, journalEntry{Event: "lost", Detail: "the slot is not waiting in the resumed experiment", ChargedUSD: &usd, CostKnown: known}, l); err != nil {
			r.failAccounting(err)
		}
		c.mu.Unlock()
		c.bookSend(bookOp{id: rec.ID, end: true, why: "lost"})
		r.count(func(s *Summary) { s.LeaseLost++ })
		return
	}
	l.slot = slot
	r.reserveForced(l.est)
	c.remove(l.archive) // an upload after the restart starts over at offset 0
	herr := error(nil)
	if !p.expired {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), cleanupTimeout)
		herr = c.store.Heartbeat(ctx, rec.SlotID, rec.Owner)
		cancel()
	}
	c.mu.Lock()
	delete(c.pending, rec.ID)
	c.leases[l.id] = l
	if p.lastBeat.After(l.lastBeat) { // a heartbeat during the re-attach counts
		l.lastBeat = p.lastBeat
	}
	w := c.workers[l.worker]
	if w != nil && !p.expired {
		found := false
		for _, s := range w.info.Running {
			found = found || s == rec.SlotID
		}
		if !found {
			w.info.Running = append(w.info.Running, rec.SlotID)
		}
		w.info.State = schemas.WorkerRunning
	}
	switch {
	case p.expired:
		c.loseLocked(l, "kept across a server restart, but its worker did not call again in time")
	case herr != nil:
		c.loseLocked(l, "kept across a server restart, but the slot's store lease is no longer this lease's: "+herr.Error())
	case w == nil:
		c.loseLocked(l, "kept across a server restart, but its worker is gone")
	default:
		c.logger.Info("lease re-attached after a server restart", "experiment", r.m.ExpID, "slot", rec.SlotID, "worker", l.worker, "lease", l.id)
	}
	live := c.leases[l.id] == l
	c.mu.Unlock()
	if live {
		r.count(func(s *Summary) { s.Reattached++ })
		r.emit(Event{Kind: EventStage, SlotID: rec.SlotID, Stage: schemas.StageLeased,
			Message: fmt.Sprintf("re-attached to remote worker %s after a server restart (attempt %d)", l.worker, l.attempt)})
	}
}

// suspending reports whether r stopped because the server is shutting
// down, with a lease book to keep its leases in.
func (c *Coordinator) suspending(r *schedRun) bool {
	return c.opts.Leases != nil && isShutdown(context.Cause(r.ctx))
}

// takeByID takes one pending slot out of the queue, whatever the run
// control gate holds back (a kept lease's run is already running).
func (q *slotQueue) takeByID(id string) (*schemas.Slot, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, slot := range q.pending {
		if slot.SlotID == id {
			q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
			q.remote++
			q.notifyLocked()
			return slot, true
		}
	}
	return nil, false
}

// reserveForced sets a kept lease's reservation aside again, whatever the
// cap says: its run is already spending.
func (r *schedRun) reserveForced(est float64) {
	if r.capUSD() <= 0 {
		return
	}
	r.mu.Lock()
	r.reserved += est
	r.inflight++
	r.mu.Unlock()
}

// journalEndedLeases reads which leases of r's journal have an event after
// "leased" (ended, charged, accepted).
func (r *schedRun) journalEndedLeases() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(r.journalDir(), r.m.ExpID+".jsonl"))
	if err != nil {
		return out
	}
	for _, line := range bytes.Split(b, []byte("\n")) {
		var e journalEntry
		if json.Unmarshal(line, &e) == nil && e.Event != "leased" && e.Lease != "" {
			out[e.Lease] = true
		}
	}
	return out
}

// journalDir is the folder of r's remote journal.
func (r *schedRun) journalDir() string {
	if r.opts.Remote != nil && r.opts.Remote.opts.StagingDir != "" {
		return r.opts.Remote.opts.StagingDir
	}
	return filepath.Join(r.s.reg.Store.Root(), stagingFolder)
}

// isShutdown reports a stop for a server shutdown (the job runner's
// ErrShutdown), as opposed to an operator's cancel.
func isShutdown(cause error) bool { return cause != nil && errors.Is(cause, jobs.ErrShutdown) }

// recordLocked is the lease book record of l. The caller holds mu.
func (c *Coordinator) recordLocked(l *remoteLease) LeaseRecord {
	rec := LeaseRecord{ID: l.id, OrgID: c.opts.OrgID, ExpID: l.r.m.ExpID, SlotID: l.slot.SlotID, WorkerID: l.worker, Owner: l.owner,
		Attempt: l.attempt, ReservedUSD: l.est, GPUs: l.gpus, GPUIDs: append([]string(nil), l.gpuIDs...), LastBeat: l.lastBeat.UTC()}
	if w := c.workers[l.worker]; w != nil {
		rec.Worker, rec.Parts, rec.Scope = c.infoLocked(w), copyStrings(w.parts), w.scope
		rec.Worker.Running = nil
	}
	return rec
}
