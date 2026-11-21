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
// of the pack's current stamp; else the last full summary while a refresh
// runs; else waits up to suiteListBudget for a fresh load and, failing that,
// serves the cheap head summary (suite.yaml and suite.lock only).
const (
	// suiteListBudgetDefault is how long GET /suites waits for one uncached
	// pack. Every pack has its own budget; the waits run side by side.
	suiteListBudgetDefault = 4 * time.Second
	// suiteSummaryTimeout bounds one background full summary.
	suiteSummaryTimeout = 15 * time.Minute
	// suiteLoadParallel is the most full summaries built at once.
	suiteLoadParallel = 2
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
	mu    sync.Mutex
	slots map[string]*summarySlot
	sem   chan struct{}
}

type summarySlot struct {
	have  bool // sum holds a full summary of stamp
	stamp string
	sum   schemas.SuiteSummary

	loadStamp string        // stamp of the refresh in flight, "" for none
	loading   chan struct{} // closed when that refresh ends
}

var suiteSummaries = newSummaryCache(suiteLoadParallel)

// suiteListBudget is suiteListBudgetDefault; tests shorten it.
var suiteListBudget = suiteListBudgetDefault

func newSummaryCache(parallel int) *summaryCache {
	return &summaryCache{slots: map[string]*summarySlot{}, sem: make(chan struct{}, parallel)}
}

// summaryLoad builds a full summary under ctx. ok false means the result is
// not worth caching (the load ran out of time).
type summaryLoad func(ctx context.Context) (sum schemas.SuiteSummary, ok bool)

// get returns the full summary of dir at stamp, or the last one while a
// refresh runs. With neither it starts a load and waits up to wait for it.
// found is false when nothing could be served in time.
func (c *summaryCache) get(dir, stamp string, load summaryLoad, wait time.Duration) (schemas.SuiteSummary, bool) {
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
	done := c.refreshLocked(dir, slot, stamp, load)
	if slot.have { // stale: served while the refresh runs
		sum := slot.sum
		c.mu.Unlock()
		return sum, true
	}
	c.mu.Unlock()
	if wait <= 0 {
		return schemas.SuiteSummary{}, false
	}
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

// refreshLocked starts a background load of dir at stamp unless one is in
// flight for it, and returns the channel closed when it ends. c.mu is held.
func (c *summaryCache) refreshLocked(dir string, slot *summarySlot, stamp string, load summaryLoad) chan struct{} {
	if slot.loading != nil && slot.loadStamp == stamp {
		return slot.loading
	}
	done := make(chan struct{})
	slot.loadStamp, slot.loading = stamp, done
	go func() {
		defer close(done)
		c.sem <- struct{}{}
		defer func() { <-c.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), suiteSummaryTimeout)
		defer cancel()
		sum, ok := load(ctx)
		c.mu.Lock()
		defer c.mu.Unlock()
		if ok {
			slot.have, slot.stamp, slot.sum = true, stamp, sum
		}
		if slot.loading == done {
			slot.loadStamp, slot.loading = "", nil
		}
	}()
	return done
}

// cachedSummary is the list entry of suite id through the cache, or ok false
// when src has no stamper or nothing was ready within wait.
func (h *SuitesHandler) cachedSummary(id string, wait time.Duration) (schemas.SuiteSummary, bool) {
	st, ok := h.suites.(suiteStamper)
	if !ok {
		return schemas.SuiteSummary{}, false
	}
	dir, stamp := st.SuiteStamp(id)
	if h.heldOutVisible { // held-out tasks are redacted unless visible: a separate entry
		dir += "\x00held-out-visible"
	}
	return suiteSummaries.get(dir, stamp, h.fullSummary(id), wait)
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
