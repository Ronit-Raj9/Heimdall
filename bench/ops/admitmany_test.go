package ops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// multiPack copies suite/testdata/valid/mini with task m01 cloned to m02..m04.
func multiPack(t *testing.T) (string, *schemas.Suite) {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("cp", "-r", filepath.Join("..", "suite", "testdata", "valid", "mini"), root).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	pack := filepath.Join(root, "mini")
	for _, id := range []string{"m02", "m03", "m04"} {
		if out, err := exec.Command("cp", "-r", filepath.Join(pack, "tasks", "m01"), filepath.Join(pack, "tasks", id)).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		ty := filepath.Join(pack, "tasks", id, "task.yaml")
		if b, err := os.ReadFile(ty); err == nil {
			os.WriteFile(ty, []byte(strings.ReplaceAll(string(b), "m01", id)), 0o644)
		}
	}
	sy := filepath.Join(pack, "suite.yaml")
	b, _ := os.ReadFile(sy)
	os.WriteFile(sy, []byte(strings.Replace(string(b), "tasks: [m01]", "tasks: [m01, m02, m03, m04]", 1)), 0o644)
	ctx := context.Background()
	s, err := LoadSuite(ctx, root, "mini")
	if err != nil {
		t.Fatal(err)
	}
	l, err := suite.LockWith(s, time.Now().UTC(), suite.FingerprintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := suite.WriteLock(filepath.Join(pack, "suite.lock"), l); err != nil {
		t.Fatal(err)
	}
	if s, err = LoadSuite(ctx, root, "mini"); err != nil {
		t.Fatal(err)
	}
	return root, s
}

func readLock(t *testing.T, s *schemas.Suite) *schemas.SuiteLock {
	t.Helper()
	r, err := LoadSuite(context.Background(), filepath.Dir(s.Root), s.ID)
	if err != nil || r.Lock == nil {
		t.Fatal(err, "no lock")
	}
	return r.Lock
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestAdmitManyContinuesWritesPerTaskAndParallel(t *testing.T) {
	_, s := multiPack(t)
	before := readLock(t, s)
	ids := []string{"m01", "m02", "m03"}
	lock, full, err := suite.LockTasks(s, ids, time.Now().UTC(), suite.FingerprintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if full {
		t.Fatal("LockTasks fell back to a full lock")
	}
	var running, peak int32
	var mu sync.Mutex
	seenDone := map[string]int{} // task -> tasks already in the lock file when it started
	o := AdmitManyOptions{Parallel: 3, admit: func(ctx context.Context, _ suite.AdmitDeps, s *schemas.Suite, task *schemas.Task) (*schemas.AdmissionRecord, error) {
		n := atomic.AddInt32(&running, 1)
		defer atomic.AddInt32(&running, -1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		if task.ID == "m02" {
			return nil, errors.New("docker exploded")
		}
		mu.Lock()
		seenDone[task.ID]++
		mu.Unlock()
		return &schemas.AdmissionRecord{Admitted: true, At: time.Now().UTC(), Checks: []schemas.AdmissionCheck{{Name: "ok", Passed: true}}}, nil
	}}
	rep := &AdmitManyReport{Suite: s.ID, Tasks: map[string]*schemas.AdmissionRecord{}, Reasons: map[string]string{}, Order: ids, Path: filepath.Join(s.Root, "suite.lock")}
	if err := runAdmissions(context.Background(), suite.AdmitDeps{}, s, lock, ids, o, rep, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(rep.Tasks) != 3 {
		t.Fatalf("ran %d tasks, want 3 (one failing must not stop the rest)", len(rep.Tasks))
	}
	after := readLock(t, s)
	// Refused or failed tasks are drafts with a reason; static validation of
	// the fixture may refuse every clone, which still must not stop the run.
	if rep.Tasks["m02"].Admitted || after.Tasks["m02"].Status != schemas.TaskDraft || rep.Reasons["m02"] == "" {
		t.Fatalf("m02 should be draft with a reason: %+v %q", after.Tasks["m02"], rep.Reasons["m02"])
	}
	for _, id := range []string{"m01", "m03"} {
		if rep.Tasks[id].Admitted && after.Tasks[id].Status != schemas.TaskAdmitted {
			t.Fatalf("%s admitted but lock says %s", id, after.Tasks[id].Status)
		}
	}
	if len(seenDone) > 1 && peak < 2 {
		t.Fatalf("peak concurrency %d, want >= 2", peak)
	}
	// Untouched task m04: lock entry and fingerprint unchanged.
	if !equalJSON(t, before.Tasks["m04"], after.Tasks["m04"]) {
		t.Fatalf("m04 entry changed:\n%+v\n%+v", before.Tasks["m04"], after.Tasks["m04"])
	}
}

func TestLockTasksLeavesOthersUntouched(t *testing.T) {
	_, s := multiPack(t)
	before := readLock(t, s)
	// Change m04's files after locking: per-task locking of m01 must not
	// notice (it never hashes m04), so m04 keeps its old entry.
	os.WriteFile(filepath.Join(s.TaskByID("m04").Dir, "extra.txt"), []byte("drift"), 0o644)
	l, full, err := suite.LockTasks(s, []string{"m01"}, time.Now().UTC(), suite.FingerprintOptions{})
	if err != nil || full {
		t.Fatalf("err %v full %v", err, full)
	}
	for _, id := range []string{"m02", "m03", "m04"} {
		if !equalJSON(t, before.Tasks[id], l.Tasks[id]) || !equalJSON(t, before.Fingerprints.Tasks[id], l.Fingerprints.Tasks[id]) {
			t.Fatalf("%s changed", id)
		}
	}
	if !equalJSON(t, before.Probes, l.Probes) {
		t.Fatal("probes changed")
	}
}

func TestAdmitIDs(t *testing.T) {
	_, s := multiPack(t)
	ids, err := admitIDs(s, []string{"m02", " m01 ", "m02"})
	if err != nil || strings.Join(ids, ",") != "m02,m01" {
		t.Fatalf("%v %v", ids, err)
	}
	if _, err := admitIDs(s, []string{"m01", "nope"}); err == nil {
		t.Fatal("unknown task accepted")
	}
}
