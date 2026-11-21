package handlers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// stampedSuites is a fakeSuites with the cache side: a settable stamp per
// suite, a head with taskCount tasks, and a LoadSuite that can be held.
type stampedSuites struct {
	*fakeSuites
	dir       string
	mu        sync.Mutex
	stamp     string
	taskCount int
	gate      chan struct{} // LoadSuite waits on it when non-nil
	loads     atomic.Int32
}

func (s *stampedSuites) SuiteStamp(id string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir + "/" + id, s.stamp
}

func (s *stampedSuites) SuiteHead(id string) (*schemas.Suite, error) {
	ids := make([]string, s.taskCount)
	for i := range ids {
		ids[i] = "t"
	}
	return &schemas.Suite{ID: id, Version: "9.9.9", TaskIDs: ids, Lock: &schemas.SuiteLock{Suite: id, Tasks: map[string]schemas.TaskLock{
		"a": {Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}},
	}}}, nil
}

func (s *stampedSuites) LoadSuite(ctx context.Context, id string) (*schemas.Suite, error) {
	s.loads.Add(1)
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.fakeSuites.LoadSuite(ctx, id)
}

func (s *stampedSuites) set(stamp string, gate chan struct{}) {
	s.mu.Lock()
	s.stamp, s.gate = stamp, gate
	s.mu.Unlock()
}

func shortBudget(t *testing.T, d time.Duration) {
	old := suiteListBudget
	suiteListBudget = d
	t.Cleanup(func() { suiteListBudget = old })
}

// The cache loads once per stamp, serves the old entry while a new stamp
// refreshes, and swaps in the new one when the refresh lands.
func TestSummaryCacheInvalidatesOnStamp(t *testing.T) {
	c := newSummaryCache(2)
	var loads atomic.Int32
	release := make(chan struct{})
	load := func(v string, gate chan struct{}) summaryLoad {
		return func(ctx context.Context) (schemas.SuiteSummary, bool) {
			loads.Add(1)
			if gate != nil {
				<-gate
			}
			return schemas.SuiteSummary{ID: "s", Version: v}, true
		}
	}
	if sum, ok := c.get("/d", "m1", load("1", nil), time.Second); !ok || sum.Version != "1" {
		t.Fatalf("first get: %v %+v", ok, sum)
	}
	if sum, ok := c.get("/d", "m1", load("x", nil), time.Second); !ok || sum.Version != "1" || loads.Load() != 1 {
		t.Fatalf("cached get: %v %+v loads=%d", ok, sum, loads.Load())
	}
	// mtime changed: the old entry is served at once while the refresh runs.
	start := time.Now()
	if sum, ok := c.get("/d", "m2", load("2", release), time.Second); !ok || sum.Version != "1" {
		t.Fatalf("stale get: %v %+v", ok, sum)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("stale get waited %s", time.Since(start))
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		sum, _ := c.get("/d", "m2", load("y", nil), 0)
		if sum.Version == "2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refresh never landed: %+v", sum)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if loads.Load() != 2 {
		t.Errorf("loads = %d, want 2", loads.Load())
	}
}

// A load that runs out of time is not cached; the next get retries.
func TestSummaryCacheSkipsTimedOutLoad(t *testing.T) {
	c := newSummaryCache(1)
	if _, ok := c.get("/d", "m", func(context.Context) (schemas.SuiteSummary, bool) { return schemas.SuiteSummary{}, false }, time.Second); ok {
		t.Fatal("timed-out load served")
	}
	if sum, ok := c.get("/d", "m", func(context.Context) (schemas.SuiteSummary, bool) { return schemas.SuiteSummary{ID: "ok"}, true }, time.Second); !ok || sum.ID != "ok" {
		t.Fatalf("retry: %v %+v", ok, sum)
	}
}

// GET /suites over a pack whose load never finishes answers within the
// per-suite budget with the cheap head entry (task count, lock state,
// admitted count), never a deadline error. Once a full summary is cached it
// is served even while a changed pack reloads slowly.
func TestSuitesListServesHeadAndCachedOnDeadline(t *testing.T) {
	shortBudget(t, 200*time.Millisecond)
	src := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 5000}
	id := ""
	for _, x := range src.ids {
		if src.loadErrs[x] == nil && id == "" {
			id = x
		}
	}
	held := make(chan struct{})
	src.set("m1", held) // held: the load does not finish until m2
	c := newSuitesClient(t, src, nil, nil)

	start := time.Now()
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil))
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("list took %s with held loads", took)
	}
	s := suiteByID(t, body, id)
	if s["error"] != nil || s["task_count"] != float64(5000) || s["lock_state"] != schemas.SuiteLocked || s["admitted_count"] != float64(1) || s["version"] != "9.9.9" {
		t.Fatalf("head entry: %v", s)
	}

	// Let the loads finish at a new stamp, then wait until every suite's full
	// entry is cached at m2 with no refresh in flight. Waiting on the served
	// versions alone is not enough: a load held at m1 can land after the gate
	// opens, so the list shows full m1 entries while the m2 refresh still runs
	// (and loads again after the count below is taken).
	src.set("m2", nil)
	close(held) // frees the cache's load slots
	deadline := time.Now().Add(30 * time.Second)
	var full map[string]any
	for {
		body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil))
		full = suiteByID(t, body, id)
		done := true
		for _, x := range src.ids {
			done = done && suiteByID(t, body, x)["version"] != "9.9.9" && settledAt(src.dir+"/"+x, "m2")
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("full entry never cached: %v", full)
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := full["version"]
	loads := src.loads.Load()

	// Same stamp: served from the cache, no load.
	suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), id)
	if src.loads.Load() != loads {
		t.Errorf("cached list loaded again: %d -> %d", loads, src.loads.Load())
	}

	// Changed pack whose reload is held: the cached entry, not an error, and
	// without waiting on the held load. The budget is long here so a list that
	// waited for the reload would take at least that long, far past the bound
	// below even on a slow machine; a timing threshold near the budget flaked.
	held3 := make(chan struct{})
	t.Cleanup(func() { close(held3) })
	src.set("m3", held3)
	suiteListBudget = 20 * time.Second
	start = time.Now()
	s = suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), id)
	if s["error"] != nil || s["version"] != want || s["tasks"] == nil {
		t.Fatalf("stale entry: %v", s)
	}
	if took := time.Since(start); took > suiteListBudget/2 {
		t.Errorf("stale entry waited %s", took)
	}
}

// settledAt reports whether the process cache holds a full summary of dir at
// stamp with no refresh in flight.
func settledAt(dir, stamp string) bool {
	suiteSummaries.mu.Lock()
	defer suiteSummaries.mu.Unlock()
	slot := suiteSummaries.slots[dir]
	return slot != nil && slot.have && slot.stamp == stamp && slot.loading == nil
}
