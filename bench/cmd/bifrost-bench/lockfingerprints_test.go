package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestFingerprintRow(t *testing.T) {
	locked := &schemas.Suite{ID: "web", Lock: &schemas.SuiteLock{Fingerprints: &schemas.LockFingerprints{}}}
	legacy := &schemas.Suite{ID: "web", Lock: &schemas.SuiteLock{}}
	drift := []schemas.FingerprintDrift{{Scope: "task", ID: "t01", Part: "files"}}
	tests := []struct {
		name   string
		s      *schemas.Suite
		drift  []schemas.FingerprintDrift
		warns  []string
		status schemas.DoctorStatus
		detail string
	}{
		{"pass", locked, nil, nil, schemas.DoctorPass, "fingerprinted, no drift"},
		{"legacy", legacy, nil, []string{suite.LegacyLockWarning}, schemas.DoctorWarn, suite.LegacyLockWarning},
		{"drift", locked, drift, nil, schemas.DoctorFail, "task t01: files changed"},
		{"unchecked image", locked, nil, []string{"suite web: image base not checked"}, schemas.DoctorWarn, "image base not checked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := fingerprintRow(tt.s, tt.drift, tt.warns, nil)
			if row.ID != "suite web lock fingerprints" || row.Status != tt.status || !strings.Contains(row.Detail, tt.detail) {
				t.Fatalf("row %+v", row)
			}
		})
	}
}

// TestLockFingerprintsKeepOldExperimentsValid is the migration proof on a
// copy of the committed coding-core pack: `suite lock` adds fingerprints
// without touching the legacy fields, keeps every admission, re-locks to the
// same bytes, leaves every recorded manifest verifying and the leaderboard
// over the recorded experiments unchanged, and from then on a changed
// verifier command refuses to plan.
func TestLockFingerprintsKeepOldExperimentsValid(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join("..", "..", "benchmarks", "coding-core")
	if _, err := os.Stat(filepath.Join(src, "suite.yaml")); err != nil {
		t.Skipf("coding-core not readable: %v", err)
	}
	board := func() *schemas.Leaderboard {
		t.Helper()
		root := t.TempDir()
		if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "testdata", "recorded"))); err != nil {
			t.Fatal(err)
		}
		st, err := store.NewFSStore(root, nil)
		if err != nil {
			t.Fatal(err)
		}
		ix := store.NewIndex(st)
		if err := ix.Rebuild(ctx); err != nil {
			t.Fatal(err)
		}
		lb, err := leaderboard.Build(ctx, handlers.LeaderboardSource(indexAdapter{ix: ix, root: st.Root()}), leaderboard.Query{})
		if err != nil {
			t.Fatal(err)
		}
		lb.GeneratedAt = lb.GeneratedAt.UTC().Truncate(0)
		return lb
	}
	boardBefore := board()

	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	dst := filepath.Join(dir, "benchmarks", "coding-core")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dst, "suite.lock")
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var old schemas.SuiteLock
	if err := json.Unmarshal(before, &old); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"lock", "coding-core", "-config", config}); code != exitOK {
		t.Fatalf("lock exit %d", code)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var fresh schemas.SuiteLock
	if err := json.Unmarshal(after, &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Fingerprints == nil {
		t.Fatal("suite lock wrote no fingerprints")
	}
	legacy, err := suite.LegacyLockHash(&fresh)
	if err != nil {
		t.Fatal(err)
	}
	if old.Fingerprints == nil && legacy != utils.SHA256Bytes(before) {
		t.Fatalf("legacy fields changed: legacy hash %s, committed lock %s", legacy, utils.SHA256Bytes(before))
	}
	for id, tl := range old.Tasks {
		if fresh.Tasks[id].Status != tl.Status || !reflect.DeepEqual(fresh.Tasks[id].Admission, tl.Admission) {
			t.Fatalf("task %s: status %s -> %s or admission changed", id, tl.Status, fresh.Tasks[id].Status)
		}
	}
	if code := runSuite([]string{"lock", "coding-core", "-config", config}); code != exitOK {
		t.Fatalf("second lock exit %d", code)
	}
	again, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, after) {
		t.Fatal("re-locking an unchanged fingerprinted pack changed suite.lock")
	}

	// Recorded manifests still verify, and the leaderboard over them is the same.
	manifests, err := filepath.Glob(filepath.Join("..", "..", "testdata", "recorded", "*", "manifest.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no recorded manifests: %v", err)
	}
	for _, p := range manifests {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var m schemas.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if h, err := engine.ManifestHash(&m); err != nil || h != m.Hash {
			t.Fatalf("%s no longer verifies: %s vs %s (%v)", p, h, m.Hash, err)
		}
	}
	boardAfter := board()
	boardAfter.GeneratedAt = boardBefore.GeneratedAt
	if !reflect.DeepEqual(boardBefore, boardAfter) || len(boardAfter.Entries) == 0 {
		t.Fatalf("leaderboard changed or empty: %d entries before, %d after", len(boardBefore.Entries), len(boardAfter.Entries))
	}

	// A changed verifier command now refuses to plan.
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	exp := &schemas.Experiment{
		Name: "fingerprint-proof", Suite: "coding-core@1.0.0", Base: "agents/toy.yaml",
		Arms:       map[string]schemas.ArmSpec{"A": {Label: "a"}, "B": {Label: "b", Set: map[string]any{"model": "other"}}},
		Primary:    schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
		Conditions: []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1}},
		Seed:       1,
	}
	if _, err := a.newPlanner().Plan(ctx, exp); err != nil && strings.Contains(err.Error(), "fingerprints drifted") {
		t.Fatalf("an unchanged pack drifted: %v", err)
	}
	editSuiteYAML(t, filepath.Join(dst, "suite.yaml"), `"--junitxml=/grader/out/junit.xml"`, `"-x", "--junitxml=/grader/out/junit.xml"`)
	_, err = a.newPlanner().Plan(ctx, exp)
	if err == nil || !strings.Contains(err.Error(), "suite lock fingerprints drifted, refusing to plan: suite coding-core: settings changed (verifier)") {
		t.Fatalf("a changed verifier command did not refuse to plan: %v", err)
	}
}

func editSuiteYAML(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}
