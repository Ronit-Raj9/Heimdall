package handlers

import (
	"context"
	"strings"
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
	if sum, ok := c.get("/d", "m1", load("1", nil), time.Second, false); !ok || sum.Version != "1" {
		t.Fatalf("first get: %v %+v", ok, sum)
	}
	if sum, ok := c.get("/d", "m1", load("x", nil), time.Second, false); !ok || sum.Version != "1" || loads.Load() != 1 {
		t.Fatalf("cached get: %v %+v loads=%d", ok, sum, loads.Load())
	}
	// mtime changed: with no wait the old entry is served at once while the
	// refresh runs (a quick pack waits for it: TestSummaryCacheWaitsForFresh...).
	start := time.Now()
	if sum, ok := c.get("/d", "m2", load("2", release), 0, false); !ok || sum.Version != "1" {
		t.Fatalf("stale get: %v %+v", ok, sum)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("stale get waited %s", time.Since(start))
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		sum, _ := c.get("/d", "m2", load("y", nil), 0, false)
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
	if _, ok := c.get("/d", "m", func(context.Context) (schemas.SuiteSummary, bool) { return schemas.SuiteSummary{}, false }, time.Second, false); ok {
		t.Fatal("timed-out load served")
	}
	if sum, ok := c.get("/d", "m", func(context.Context) (schemas.SuiteSummary, bool) { return schemas.SuiteSummary{ID: "ok"}, true }, time.Second, false); !ok || sum.ID != "ok" {
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

	// Changed pack whose reload is held: the cached entry, not an error. The
	// pack loaded quickly before, so the list waits for the reload up to the
	// budget (a write shows on the next read) and then serves the old entry.
	held3 := make(chan struct{})
	t.Cleanup(func() { close(held3) })
	src.set("m3", held3)
	suiteListBudget = 300 * time.Millisecond
	start = time.Now()
	s = suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), id)
	if s["error"] != nil || s["version"] != want || s["tasks"] == nil {
		t.Fatalf("stale entry: %v", s)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("stale entry waited %s, past the budget", took)
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

// cacheGet is get with the heavy flag (a library pack takes the library
// load slot).
func cacheGet(c *summaryCache, dir, stamp string, load summaryLoad, wait time.Duration, heavy bool) (schemas.SuiteSummary, bool) {
	return c.get(dir, stamp, load, wait, heavy)
}

// versionLoad is a summary load of version v that takes d.
func versionLoad(v string, d time.Duration) summaryLoad {
	return func(ctx context.Context) (schemas.SuiteSummary, bool) {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return schemas.SuiteSummary{}, false
		}
		return schemas.SuiteSummary{ID: "s", Version: v}, true
	}
}

// After a write moves a small pack's stamp, the next read waits for the
// refresh (it is quick) and serves the new summary, not the old one (N116 to
// N118).
func TestSummaryCacheWaitsForFreshAfterStampChange(t *testing.T) {
	c := newSummaryCache(2)
	if sum, ok := cacheGet(c, "/fresh", "m1", versionLoad("1", 0), time.Second, false); !ok || sum.Version != "1" {
		t.Fatalf("first get: %v %+v", ok, sum)
	}
	sum, ok := cacheGet(c, "/fresh", "m2", versionLoad("2", 50*time.Millisecond), 3*time.Second, false)
	if !ok || sum.Version != "2" {
		t.Fatalf("after a stamp change got %v %+v, want the fresh summary 2", ok, sum)
	}
}

// A pack whose last load took longer than the wait serves the old summary at
// once while it refreshes; a quick pack whose refresh overruns the wait
// serves the old one after the wait.
func TestSummaryCacheServesStaleWhenSlow(t *testing.T) {
	c := newSummaryCache(2)
	if _, ok := cacheGet(c, "/slow", "m1", versionLoad("1", 300*time.Millisecond), 5*time.Second, false); !ok {
		t.Fatal("first get")
	}
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	held := func(ctx context.Context) (schemas.SuiteSummary, bool) {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return schemas.SuiteSummary{}, false
	}
	start := time.Now()
	sum, ok := cacheGet(c, "/slow", "m2", held, 100*time.Millisecond, false)
	if !ok || sum.Version != "1" {
		t.Fatalf("slow pack: %v %+v, want the stale summary", ok, sum)
	}
	if took := time.Since(start); took > 80*time.Millisecond {
		t.Errorf("slow pack waited %s for a refresh it cannot finish in time", took)
	}

	if _, ok := cacheGet(c, "/quick", "m1", versionLoad("1", 0), time.Second, false); !ok {
		t.Fatal("quick first get")
	}
	start = time.Now()
	sum, ok = cacheGet(c, "/quick", "m2", held, 150*time.Millisecond, false)
	if !ok || sum.Version != "1" {
		t.Fatalf("quick pack, held refresh: %v %+v, want the stale summary", ok, sum)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("quick pack waited %s, want about the wait", took)
	}
}

// A second list while the first one's load still runs answers at once with
// the head summary: a refresh in flight is never waited for twice (E1).
func TestSuitesListDoesNotWaitTwiceForInFlightLoad(t *testing.T) {
	shortBudget(t, time.Second)
	src := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 7}
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	src.set("m1", held)
	c := newSuitesClient(t, src, nil, nil)
	c.do(t, "GET", APIPrefix+"/suites", nil, nil) // waits the budget once
	start := time.Now()
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil))
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("second list took %s while the load was in flight", took)
	}
	if s := suiteByID(t, body, "canary"); s["task_count"] != float64(7) {
		t.Fatalf("head entry: %v", s)
	}
}

// Library loads take their own slot: two slow library packs never hold up
// an organization pack's refresh.
func TestLibraryLoadDoesNotBlockOrgRefresh(t *testing.T) {
	c := newSummaryCache(2)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	heavy := func(ctx context.Context) (schemas.SuiteSummary, bool) {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return schemas.SuiteSummary{}, false
	}
	cacheGet(c, "/lib1", "m", heavy, 0, true)
	cacheGet(c, "/lib2", "m", heavy, 0, true)
	cacheGet(c, "/lib3", "m", heavy, 0, true)
	time.Sleep(20 * time.Millisecond) // the library loads take their slots
	sum, ok := cacheGet(c, "/org", "m", versionLoad("1", 0), 2*time.Second, false)
	if !ok || sum.Version != "1" {
		t.Fatalf("org pack behind library loads: %v %+v", ok, sum)
	}
}

// ?fields=light lists every pack without per-task lists and never waits.
func TestSuitesLightListHasNoTaskArrays(t *testing.T) {
	shortBudget(t, 5*time.Second)
	src := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 3}
	src.set("m1", nil)
	c := newSuitesClient(t, src, nil, nil)
	full := suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), "canary")
	if tasks, _ := full["tasks"].([]any); len(tasks) == 0 {
		t.Fatalf("full list has no tasks: %v", full)
	}
	resp := c.do(t, "GET", APIPrefix+"/suites?fields=light", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("light list status %d: %s", resp.StatusCode(), resp.Body())
	}
	body := decodeBody(t, resp)
	for _, id := range []string{"canary", "drifted"} {
		s := suiteByID(t, body, id)
		for _, k := range []string{"tasks", "drift", "probes"} {
			if v, ok := s[k].([]any); !ok || len(v) != 0 {
				t.Errorf("%s %s = %v, want []", id, k, s[k])
			}
		}
		for _, k := range []string{"fingerprint_drift", "artifacts", "tiers"} {
			if _, ok := s[k]; ok {
				t.Errorf("%s has %s in the light list", id, k)
			}
		}
		if s["version"] == "" || s["task_count"] == nil {
			t.Errorf("%s light entry lost its facts: %v", id, s)
		}
	}
	if s := suiteByID(t, body, "canary"); s["task_count"] != full["task_count"] || s["lock_state"] != full["lock_state"] {
		t.Errorf("light canary %v, full %v", s, full)
	}

	// A pack whose load is held answers at once with its head entry.
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	fresh := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 4}
	fresh.set("m1", held)
	c2 := newSuitesClient(t, fresh, nil, nil)
	start := time.Now()
	body = decodeBody(t, c2.do(t, "GET", APIPrefix+"/suites?fields=light", nil, nil))
	if took := time.Since(start); took > time.Second {
		t.Errorf("light list waited %s for a held load", took)
	}
	if s := suiteByID(t, body, "canary"); s["task_count"] != float64(4) {
		t.Errorf("held pack light entry: %v", s)
	}
}

func TestSuitesFieldsBadValue(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), nil, nil)
	resp := c.do(t, "GET", APIPrefix+"/suites?fields=tasks", nil, nil)
	if resp.StatusCode() != 400 || !strings.Contains(string(resp.Body()), "fields must be light or full") {
		t.Fatalf("status %d body %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "GET", APIPrefix+"/suites?fields=full", nil, nil); resp.StatusCode() != 200 {
		t.Fatalf("fields=full: %d", resp.StatusCode())
	}
}
