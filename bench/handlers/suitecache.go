package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
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
	st, ok := h.suites.(suiteStamper)
	if !ok {
		return schemas.SuiteSummary{}, false
	}
	dir, stamp := st.SuiteStamp(id)
	if h.heldOutVisible { // held-out tasks are redacted unless visible: a separate entry
		dir += "\x00held-out-visible"
	}
	return suiteSummaries.get(dir, stamp, h.fullSummary(id), wait, isBuiltinSuite(h.suites, id))
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

// WarmCache starts building the full summary of every pack in the
// background, so the first GET /suites after start serves the large library
// packs from the cache instead of waiting on them. It returns at once.
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
		for _, id := range ids {
			h.cachedSummary(id, 0)
		}
	}()
	return h
}
