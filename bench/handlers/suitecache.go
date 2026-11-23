package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// Suite list cache. A full suite summary loads every task file and rehashes
// the pack for lock drift: minutes for a built-in library pack with
// thousands of tasks and gigabytes of audio. GET /suites therefore never
// loads a pack in the request. It serves, per pack, the cached full summary
// of the pack's current stamp. After a change (a stale summary) it waits for
// the refresh when the pack's last load fit in the wait, so a write to a
// small organization pack shows on the next read, and serves the old summary
// at once for a slow pack. With no summary at all it waits up to
// suiteListBudget once per refresh and, failing that, serves the cheap head
// summary (suite.yaml and suite.lock only).
const (
	// suiteListBudgetDefault is how long GET /suites waits for one uncached
	// pack. Every pack has its own budget; the waits run side by side.
	suiteListBudgetDefault = 4 * time.Second
	// suiteSummaryTimeout bounds one background full summary.
	suiteSummaryTimeout = 15 * time.Minute
	// suiteLoadParallel is the most organization pack summaries built at
	// once; suiteLibraryLoadParallel the most library pack summaries. The
	// two pools are separate, so a library rebuild never delays an
	// organization pack.
	suiteLoadParallel        = 2
	suiteLibraryLoadParallel = 1
)

// suiteStamper is the optional SuiteSource side the list cache needs
// (cmd/bifrost-bench servesuitecache.go). A source without it is loaded per
// request, as before.
type suiteStamper interface {
	// SuiteStamp returns the folder suite id loads from and a stamp that
	// changes when suite.yaml, suite.lock or the task folders change.
	SuiteStamp(id string) (dir, stamp string)
	// SuiteHead loads suite.yaml and suite.lock only: no task file is read.
	SuiteHead(id string) (*schemas.Suite, error)
}

// summaryCache holds full suite summaries process-wide, keyed by pack folder:
// library packs are shared by every organization's handler.
type summaryCache struct {
	mu     sync.Mutex
	slots  map[string]*summarySlot
	orgSem chan struct{} // load slots of organization packs
	libSem chan struct{} // load slots of library packs (heavy)
}

type summarySlot struct {
	have  bool // sum holds a full summary of stamp
	stamp string
	sum   schemas.SuiteSummary

	loadStamp string        // stamp of the refresh in flight, "" for none
	loading   chan struct{} // closed when that refresh ends

	// lastLoad is how long the last finished load took (zero before the
	// first one): a stale read waits for a refresh only when the pack loads
	// within the wait.
	lastLoad time.Duration
}

var suiteSummaries = newSummaryCache(suiteLoadParallel)

// suiteListBudget is suiteListBudgetDefault; tests shorten it.
var suiteListBudget = suiteListBudgetDefault

// newSummaryCache makes a cache with parallel organization load slots and
// suiteLibraryLoadParallel library load slots.
func newSummaryCache(parallel int) *summaryCache {
	return &summaryCache{
		slots:  map[string]*summarySlot{},
		orgSem: make(chan struct{}, parallel),
		libSem: make(chan struct{}, suiteLibraryLoadParallel),
	}
}

// summaryLoad builds a full summary under ctx. ok false means the result is
// not worth caching (the load ran out of time).
type summaryLoad func(ctx context.Context) (sum schemas.SuiteSummary, ok bool)

// get returns the full summary of dir at stamp. found is false when nothing
// could be served in time.
//
//   - Fresh (a summary of stamp): returned at once.
//   - Stale (a summary of another stamp): the refresh is started or joined.
//     When the pack's last load took no longer than wait, get waits for it up
//     to wait and returns the new summary if it landed, else the stale one;
//     a slow or never measured pack gets the stale one at once.
//   - None: when a refresh of stamp was already in flight before this call,
//     not found at once (the caller serves the head summary; a load is never
//     waited for twice); else the load starts and get waits up to wait.
//
// heavy loads (library packs) take the library load slot.
func (c *summaryCache) get(dir, stamp string, load summaryLoad, wait time.Duration, heavy bool) (schemas.SuiteSummary, bool) {
	c.mu.Lock()
	slot := c.slots[dir]
	if slot == nil {
		slot = &summarySlot{}
		c.slots[dir] = slot
	}
	if slot.have && slot.stamp == stamp {
		sum := slot.sum
		c.mu.Unlock()
		return sum, true
	}
	joined := slot.loading != nil && slot.loadStamp == stamp
	done := c.refreshLocked(slot, stamp, load, heavy)
	if slot.have { // stale
		sum := slot.sum
		quick := slot.lastLoad > 0 && slot.lastLoad <= wait
		c.mu.Unlock()
		if !quick {
			return sum, true
		}
		if fresh, ok := c.await(slot, stamp, done, wait); ok {
			return fresh, true
		}
		return sum, true
	}
	c.mu.Unlock()
	if joined || wait <= 0 {
		return schemas.SuiteSummary{}, false
	}
	return c.await(slot, stamp, done, wait)
}

// await waits up to wait for done and returns slot's summary when it is of
// stamp by then.
func (c *summaryCache) await(slot *summarySlot, stamp string, done chan struct{}, wait time.Duration) (schemas.SuiteSummary, bool) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		return schemas.SuiteSummary{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot.have && slot.stamp == stamp {
		return slot.sum, true
	}
	return schemas.SuiteSummary{}, false
}

// refreshLocked starts a background load of slot at stamp unless one is in
// flight for it, and returns the channel closed when it ends. c.mu is held.
func (c *summaryCache) refreshLocked(slot *summarySlot, stamp string, load summaryLoad, heavy bool) chan struct{} {
	if slot.loading != nil && slot.loadStamp == stamp {
		return slot.loading
	}
	sem := c.orgSem
	if heavy {
		sem = c.libSem
	}
	done := make(chan struct{})
	slot.loadStamp, slot.loading = stamp, done
	go func() {
		defer close(done)
		sem <- struct{}{}
		defer func() { <-sem }()
		ctx, cancel := context.WithTimeout(context.Background(), suiteSummaryTimeout)
		defer cancel()
		start := time.Now()
		sum, ok := load(ctx)
		took := time.Since(start)
		c.mu.Lock()
		defer c.mu.Unlock()
		if ok {
			slot.have, slot.stamp, slot.sum = true, stamp, sum
			slot.lastLoad = max(took, time.Nanosecond)
		}
		if slot.loading == done {
			slot.loadStamp, slot.loading = "", nil
		}
	}()
	return done
}

// cachedSummary is the list entry of suite id through the cache, or ok false
// when src has no stamper or nothing was ready within wait. Library packs
// load in their own slot.
func (h *SuitesHandler) cachedSummary(id string, wait time.Duration) (schemas.SuiteSummary, bool) {
	dir, stamp, ok := h.summaryKey(id)
	if !ok {
		return schemas.SuiteSummary{}, false
	}
	return suiteSummaries.get(dir, stamp, h.fullSummary(id), wait, isBuiltinSuite(h.suites, id))
}

// summaryKey is the cache key of suite id's full summary: its folder and
// stamp. ok is false when the source has no stamper.
func (h *SuitesHandler) summaryKey(id string) (dir, stamp string, ok bool) {
	st, ok := h.suites.(suiteStamper)
	if !ok {
		return "", "", false
	}
	dir, stamp = st.SuiteStamp(id)
	if h.heldOutVisible { // held-out tasks are redacted unless visible: a separate entry
		dir += "\x00held-out-visible"
	}
	return dir, stamp, true
}

// fullSummary loads suite id and summarizes it, the slow path the cache
// runs in the background.
func (h *SuitesHandler) fullSummary(id string) summaryLoad {
	return func(ctx context.Context) (schemas.SuiteSummary, bool) {
		s, err := h.suites.LoadSuite(ctx, id)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return schemas.SuiteSummary{}, false
			}
			return brokenSuite(id, err), true
		}
		return h.summarize(ctx, s), ctx.Err() == nil
	}
}

// headSummary is the cheap list entry of suite id: suite.yaml and suite.lock
// only. Lock state is read from the lock file without rehashing (drift is
// checked by the full summary); tasks are counted, not listed.
func headSummary(src SuiteSource, id string) schemas.SuiteSummary {
	st, ok := src.(suiteStamper)
	if !ok {
		return brokenSuite(id, errors.New("suite summary not ready"))
	}
	s, err := st.SuiteHead(id)
	if err != nil {
		return brokenSuite(id, err)
	}
	return headOf(s)
}

// headOf is the head entry of a suite loaded by SuiteHead. It is marked
// Partial: tasks, probes and drift come with the full summary.
func headOf(s *schemas.Suite) schemas.SuiteSummary {
	out := schemas.SuiteSummary{
		ID: s.ID, Version: s.Version, Description: s.Description,
		Environment: s.Environment, Grader: s.Grader,
		VerifierResult:   verifierResult(s.Verifier),
		VerifierTimeoutS: s.Verifier.TimeoutS,
		PassThreshold:    passThreshold(s.Verifier),
		BaseImage:        schemas.SuiteImage{Tag: s.BaseImage.Tag, Digest: s.BaseImage.Digest},
		LockHash:         s.LockHash,
		LockState:        schemas.SuiteUnlocked,
		Drift:            []schemas.SuiteDrift{},
		Probes:           []schemas.SuiteProbe{},
		Tasks:            []schemas.SuiteTask{},
		TaskCount:        len(s.TaskIDs),
		Partial:          true,
	}
	scenarioSummary(s, &out) // track, grading, output: the wizard filters on them
	if s.Lock != nil {
		at := s.Lock.CreatedAt
		out.LockedAt = &at
		out.LockState = schemas.SuiteLocked
		out.LockKind = schemas.LockKindLegacy
		if s.Lock.Fingerprints != nil {
			out.LockKind = schemas.LockKindFingerprinted
		}
		if out.BaseImage.Digest == "" {
			out.BaseImage.Digest = s.Lock.BaseImage.Digest
		}
		for _, tl := range s.Lock.Tasks {
			if tl.Status == schemas.TaskAdmitted && tl.Admission != nil && tl.Admission.Admitted {
				out.AdmittedCount++
			}
		}
	}
	return out
}

// WarmCache warms the full summary of every pack in the background
// (WarmSuites: one pack at a time), so the first GET /suites after start
// serves the large library packs from the cache instead of waiting on them.
// It returns at once.
func (h *SuitesHandler) WarmCache() *SuitesHandler {
	if _, ok := h.suites.(suiteStamper); !ok {
		return h
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		ids, err := h.suites.SuiteIDs(ctx)
		cancel()
		if err != nil {
			return
		}
		<-WarmSuites(context.Background(), h, ids, nil)
	}()
	return h
}

// WarmSuites fills the full summary cache for ids in the background and
// returns at once; the channel closes when the warm ends. Packs load one at
// a time, in the order given: the next starts only after the previous one
// landed, so the warm never holds more than one load slot and a request
// queued for a slot is served before the warm's next pack. A pack that
// fails to load is logged and skipped. When ctx ends no further pack
// starts (a load already running finishes in the background, as any cache
// refresh does). At the end it logs "warmed N benchmark summaries in T".
// logger may be nil.
func WarmSuites(ctx context.Context, h *SuitesHandler, ids []string, logger schemas.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		start, n := time.Now(), 0
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			if h.warmOne(ctx, id) {
				n++
			} else if ctx.Err() == nil && logger != nil {
				logger.Warn("warm benchmark summary %s: the load did not finish; skipped", id)
			}
		}
		if logger != nil {
			logger.Info("warmed %d benchmark summaries in %s", n, time.Since(start).Round(time.Millisecond))
		}
	}()
	return done
}

// warmOne starts the full summary of suite id unless it is cached and
// waits for it to land or ctx to end. It reports whether the summary is in
// the cache.
func (h *SuitesHandler) warmOne(ctx context.Context, id string) bool {
	dir, stamp, ok := h.summaryKey(id)
	if !ok {
		return false
	}
	done, have := suiteSummaries.start(dir, stamp, h.fullSummary(id), isBuiltinSuite(h.suites, id))
	if have {
		return true
	}
	select {
	case <-done:
	case <-ctx.Done():
		return false
	}
	return suiteSummaries.has(dir, stamp)
}

// start begins (or joins) the refresh of dir at stamp and returns its end
// channel; have is true, with no channel, when a summary of stamp is
// already cached.
func (c *summaryCache) start(dir, stamp string, load summaryLoad, heavy bool) (done chan struct{}, have bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot := c.slots[dir]
	if slot == nil {
		slot = &summarySlot{}
		c.slots[dir] = slot
	}
	if slot.have && slot.stamp == stamp {
		return nil, true
	}
	return c.refreshLocked(slot, stamp, load, heavy), false
}

// has reports whether a summary of dir at stamp is cached.
func (c *summaryCache) has(dir, stamp string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot := c.slots[dir]
	return slot != nil && slot.have && slot.stamp == stamp
}

// readyHeadSlot is a cached head entry with the ready tasks read from the lock.
type readyHeadSlot struct {
	stamp string
	sum   schemas.SuiteSummary
	ready []string
	known bool
}

// suiteHeadCache caches head entries process-wide, keyed by pack folder and
// checked against the pack's stamp, so the light list reads suite.yaml and
// suite.lock once per change, not once per request.
var suiteHeadCache = struct {
	mu sync.Mutex
	m  map[string]readyHeadSlot
}{m: map[string]readyHeadSlot{}}

// readyHead is suite id's head entry (headOf) with its ready task ids from
// the lock (readyFromLock). known is false when the head could not be read;
// the entry is then the broken-suite entry and is not cached.
func (h *SuitesHandler) readyHead(id string) (sum schemas.SuiteSummary, ready []string, known bool) {
	st, ok := h.suites.(suiteStamper)
	if !ok {
		return headSummary(h.suites, id), nil, false
	}
	dir, stamp := st.SuiteStamp(id)
	suiteHeadCache.mu.Lock()
	e, hit := suiteHeadCache.m[dir]
	suiteHeadCache.mu.Unlock()
	if hit && e.stamp == stamp {
		return e.sum, e.ready, e.known
	}
	s, err := st.SuiteHead(id)
	if err != nil {
		return brokenSuite(id, err), nil, false
	}
	e = readyHeadSlot{stamp: stamp, sum: headOf(s), ready: readyFromLock(s), known: true}
	suiteHeadCache.mu.Lock()
	suiteHeadCache.m[dir] = e
	suiteHeadCache.mu.Unlock()
	return e.sum, e.ready, e.known
}

// readyFromLock lists the tasks of s a plan accepts, in suite order, from
// suite.yaml, suite.lock and the quarantine sidecar only: admitted, with an
// admission recorded for the current lock fingerprints (the planner's
// checkLock), and not quarantined. An unlocked suite has none.
func readyFromLock(s *schemas.Suite) []string {
	out := []string{}
	if s.Lock == nil {
		return out
	}
	var quarantined map[string]schemas.TaskQuarantine
	if s.Root != "" {
		quarantined, _ = readQuarantineFile(s.Root) // an unreadable sidecar quarantines nothing here; the planner still checks
	}
	order := s.TaskIDs
	if len(order) == 0 {
		for _, t := range s.Tasks {
			order = append(order, t.ID)
		}
	}
	for _, id := range order {
		tl, ok := s.Lock.Tasks[id]
		if !ok || tl.Status != schemas.TaskAdmitted || !suite.AdmissionCurrent(s.Lock, id, tl.Admission) {
			continue
		}
		if _, q := quarantined[id]; q {
			continue
		}
		out = append(out, id)
	}
	return out
}

// readySummary sets the light entry's ready tasks: the lock's ready ids,
// less the tasks the full summary (when cached) found changed since the
// lock. Without a readable lock a cached full summary answers from its own
// task list; else readiness stays unknown.
func readySummary(out *schemas.SuiteSummary, full bool, ready []string, known bool) {
	switch {
	case known && full:
		stale := map[string]bool{}
		for _, t := range out.Tasks {
			if t.Stale {
				stale[t.ID] = true
			}
		}
		ids := make([]string, 0, len(ready))
		for _, id := range ready {
			if !stale[id] {
				ids = append(ids, id)
			}
		}
		out.ReadyTaskIDs, out.ReadyKnown = ids, true
	case known:
		out.ReadyTaskIDs, out.ReadyKnown = append([]string(nil), ready...), true
	case full && out.Error == "":
		ids := []string{}
		for _, t := range out.Tasks {
			if t.Status == string(schemas.TaskAdmitted) && !t.Stale && t.Quarantine == nil {
				ids = append(ids, t.ID)
			}
		}
		out.ReadyTaskIDs, out.ReadyKnown = ids, true
	}
	if len(out.ReadyTaskIDs) == 0 {
		out.ReadyTaskIDs = nil // known and none: ready_known true, no list
	}
}

// forgetSuite drops every cached list entry of suite id: its full summaries
// (with and without held-out tasks) and its head entry. DELETE /suites/{id}
// calls it with the folder the pack loaded from, read before the delete, so
// the deleted pack is never served from memory and a pack created again
// under the same id starts fresh (fix wave 3, N288 with the H17 cache).
func forgetSuite(src SuiteSource, id string) {
	st, ok := src.(suiteStamper)
	if !ok {
		return
	}
	dir, _ := st.SuiteStamp(id)
	forgetSuiteDir(dir)
}

// forgetSuiteDir is forgetSuite for a pack folder already looked up.
func forgetSuiteDir(dir string) {
	if dir == "" {
		return
	}
	suiteSummaries.mu.Lock()
	delete(suiteSummaries.slots, dir)
	delete(suiteSummaries.slots, dir+"\x00held-out-visible")
	suiteSummaries.mu.Unlock()
	suiteHeadCache.mu.Lock()
	delete(suiteHeadCache.m, dir)
	suiteHeadCache.mu.Unlock()
}

// suiteDirOf is the folder suite id loads from, "" when src has no cache side.
func suiteDirOf(src SuiteSource, id string) string {
	st, ok := src.(suiteStamper)
	if !ok {
		return ""
	}
	dir, _ := st.SuiteStamp(id)
	return dir
}
