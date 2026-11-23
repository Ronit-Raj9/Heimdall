package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// hw17HeadSuites is a stampedSuites whose head is a given suite (or error).
type hw17HeadSuites struct {
	*stampedSuites
	head    *schemas.Suite
	headErr error
}

func (s *hw17HeadSuites) SuiteHead(id string) (*schemas.Suite, error) {
	if s.headErr != nil {
		return nil, s.headErr
	}
	h := *s.head
	h.ID = id
	return &h, nil
}

// hw17ReadyHead is a locked head over tasks in suite order: only a and e are
// ready. b is a draft, c is admitted but quarantined, d was admitted for
// other lock fingerprints, f is not in the lock.
func hw17ReadyHead(t *testing.T) *schemas.Suite {
	t.Helper()
	root := t.TempDir()
	q, err := json.Marshal(schemas.SuiteHealthFile{Quarantined: map[string]schemas.TaskQuarantine{"c": {Source: schemas.QuarantineManual}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, schemas.SuiteHealthFileName), q, 0o644); err != nil {
		t.Fatal(err)
	}
	ok := &schemas.AdmissionRecord{Admitted: true}
	return &schemas.Suite{
		Version: "1.0.0", Root: root, TaskIDs: []string{"e", "a", "b", "c", "d", "f"},
		Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{
			"a": {Status: schemas.TaskAdmitted, Admission: ok},
			"b": {Status: schemas.TaskDraft},
			"c": {Status: schemas.TaskAdmitted, Admission: ok},
			"d": {Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true, Fingerprint: "sha256:old"}},
			"e": {Status: schemas.TaskAdmitted, Admission: ok},
		}},
	}
}

// The light list carries the ready task ids of every pack from its lock,
// in suite order, without waiting for the full summary: admitted and
// current only; drafts, quarantined tasks and stale admissions are left out.
func TestSuitesLightListHasReadyIDsFromLock(t *testing.T) {
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	src := &hw17HeadSuites{stampedSuites: &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir()}, head: hw17ReadyHead(t)}
	src.set("m1", held)
	c := newSuitesClient(t, src, nil, nil)
	start := time.Now()
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites?fields=light", nil, nil))
	if took := time.Since(start); took > time.Second {
		t.Errorf("light list waited %s", took)
	}
	s := suiteByID(t, body, "canary")
	if s["ready_known"] != true {
		t.Fatalf("ready_known = %v, want true: %v", s["ready_known"], s)
	}
	got, _ := json.Marshal(s["ready_task_ids"])
	if string(got) != `["e","a"]` {
		t.Errorf("ready_task_ids = %s, want [\"e\",\"a\"]", got)
	}
	if s["partial"] != true {
		t.Errorf("a head entry is not marked partial: %v", s["partial"])
	}
	// The full list keeps its shape: no ready ids there.
	full := suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil)), "canary")
	if _, ok := full["ready_task_ids"]; ok {
		t.Errorf("full list carries ready_task_ids: %v", full["ready_task_ids"])
	}
}

// A pack whose lock cannot be read has unknown ready tasks: ready_known
// false and no id list (never an empty list, which would read as none
// ready).
func TestSuitesLightListReadyUnknownWithoutLock(t *testing.T) {
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	src := &hw17HeadSuites{stampedSuites: &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir()}, headErr: errors.New("suite.lock: bad yaml")}
	src.set("m1", held)
	c := newSuitesClient(t, src, nil, nil)
	s := suiteByID(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites?fields=light", nil, nil)), "canary")
	if v, ok := s["ready_known"]; ok && v != false {
		t.Errorf("ready_known = %v, want false or absent", v)
	}
	if v, ok := s["ready_task_ids"]; ok && v != nil {
		t.Errorf("ready_task_ids = %v, want absent", v)
	}
}

// The head summary (no tasks, no probes) says it is partial; a full
// summary does not.
func TestHeadSummaryIsPartial(t *testing.T) {
	src := &hw17HeadSuites{stampedSuites: &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir()}, head: hw17ReadyHead(t)}
	if h := headSummary(src, "canary"); !h.Partial {
		t.Errorf("head summary Partial = false")
	}
	h := NewSuitesHandler(testMode, src)
	sum, ok := h.fullSummary("canary")(context.Background())
	if !ok || sum.Partial {
		t.Errorf("full summary ok=%v Partial=%v", ok, sum.Partial)
	}
	data, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["partial"]; ok {
		t.Errorf("full summary JSON has partial")
	}
}

// WarmSuites returns at once, loads one pack at a time (the next starts
// only after the previous landed), fills the cache so a later read is a
// hit, and stops starting packs when its context ends.
func TestWarmSuitesFillsCacheWithoutBlocking(t *testing.T) {
	gate := make(chan struct{})
	src := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 2}
	src.set("m1", gate)
	h := NewSuitesHandler(testMode, src)
	ids := []string{"canary", "drifted"}
	start := time.Now()
	done := WarmSuites(context.Background(), h, ids, schemas.NoopLogger{})
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("WarmSuites blocked for %s", took)
	}
	time.Sleep(150 * time.Millisecond)
	if n := src.loads.Load(); n != 1 {
		t.Fatalf("loads while the first pack is held = %d, want 1 (one pack at a time)", n)
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("warm never finished")
	}
	before := src.loads.Load()
	for _, id := range ids {
		if _, ok := h.cachedSummary(id, 0); !ok {
			t.Errorf("%s not cached after the warm", id)
		}
	}
	if after := src.loads.Load(); after != before {
		t.Errorf("reads after the warm loaded again: %d -> %d", before, after)
	}

	// Shutdown: a cancelled warm ends without starting the next pack.
	gate2 := make(chan struct{})
	t.Cleanup(func() { close(gate2) })
	src2 := &stampedSuites{fakeSuites: testSuites(t), dir: t.TempDir(), taskCount: 2}
	src2.set("m1", gate2)
	ctx, cancel := context.WithCancel(context.Background())
	done2 := WarmSuites(ctx, NewSuitesHandler(testMode, src2), ids, schemas.NoopLogger{})
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled warm did not stop")
	}
	if n := src2.loads.Load(); n != 1 {
		t.Errorf("cancelled warm started %d loads, want 1", n)
	}
}
