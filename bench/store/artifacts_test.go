package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// recLogger records warnings so tests can assert a torn line was reported.
type recLogger struct {
	mu    sync.Mutex
	warns []string
}

func (l *recLogger) Debug(string, ...any) {}
func (l *recLogger) Info(string, ...any)  {}
func (l *recLogger) Error(string, ...any) {}
func (l *recLogger) Warn(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg+" "+fmt.Sprint(args...))
}

func (l *recLogger) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, w := range l.warns {
		if strings.Contains(w, substr) {
			n++
		}
	}
	return n
}

// newTestStore returns a store in a temp dir. Sealed folders are 0555, so a
// cleanup registered after TempDir's (and so run before it) makes the tree
// writable again for removal.
func newTestStore(t *testing.T, logger schemas.Logger) *FSStore {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(t, root) })
	s, err := NewFSStore(root, logger)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	return s
}

func makeWritable(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o644)
	})
	if err != nil {
		t.Logf("make writable %s: %v", root, err)
	}
}

// testSlots: a1 and b1 form pair p_1, c1 is an uncounted compatibility slot.
func testManifest(expID string) *schemas.Manifest {
	return &schemas.Manifest{
		ExpID:   expID,
		SuiteID: "coding_core",
		Hash:    "sha256:manifest",
		Slots: []schemas.Slot{
			{SlotID: "s_a1", PairID: "p_1", Task: "t01", Arm: "a", Condition: "clean", Trial: 1, OrderIndex: 0, Counted: true},
			{SlotID: "s_b1", PairID: "p_1", Task: "t01", Arm: "b", Condition: "clean", Trial: 1, OrderIndex: 1, Counted: true},
			{SlotID: "s_c1", PairID: "p_c", Task: "t01", Arm: "compat", Condition: "clean", Trial: 1, OrderIndex: 2, Counted: false},
		},
	}
}

func mustCreateExp(t *testing.T, s *FSStore, expID string) *schemas.Manifest {
	t.Helper()
	m := testManifest(expID)
	if err := s.CreateExperiment(context.Background(), m); err != nil {
		t.Fatalf("CreateExperiment: %v", err)
	}
	return m
}

func slotOf(m *schemas.Manifest, slotID string) *schemas.Slot {
	for i := range m.Slots {
		if m.Slots[i].SlotID == slotID {
			sl := m.Slots[i]
			sl.ExpID = m.ExpID
			return &sl
		}
	}
	return nil
}

func mustNewRun(t *testing.T, s *FSStore, m *schemas.Manifest, slotID, runID string, attempt int, outcome schemas.Outcome) *schemas.Run {
	t.Helper()
	sl := slotOf(m, slotID)
	r := &schemas.Run{RunID: runID, SlotID: slotID, PairID: sl.PairID, ExpID: m.ExpID, Attempt: attempt,
		Stage: schemas.StageCleanup, ManifestHash: m.Hash, Outcome: outcome}
	if _, err := s.NewRun(context.Background(), sl, r); err != nil {
		t.Fatalf("NewRun %s: %v", runID, err)
	}
	return r
}

func mustWrite(t *testing.T, s *FSStore, r *schemas.Run, name, data string) {
	t.Helper()
	if err := s.WriteRunFile(context.Background(), r, name, []byte(data)); err != nil {
		t.Fatalf("WriteRunFile %s: %v", name, err)
	}
}

func mustSeal(t *testing.T, s *FSStore, r *schemas.Run) *schemas.SealRecord {
	t.Helper()
	rec, err := s.Seal(context.Background(), r)
	if err != nil {
		t.Fatalf("Seal %s: %v", r.RunID, err)
	}
	return rec
}

func TestNewFSStore(t *testing.T) {
	if _, err := NewFSStore("", nil); err == nil {
		t.Fatal("empty root: want error")
	}
	s := newTestStore(t, nil)
	if s.GetName() != "fs" || s.GetVersion() != "1.0.0" {
		t.Fatalf("name/version = %s/%s", s.GetName(), s.GetVersion())
	}
	if !filepath.IsAbs(s.Root()) {
		t.Fatalf("root %s not absolute", s.Root())
	}
}

func TestCreateExperiment(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		mutate  func(m *schemas.Manifest)
		twice   bool
		wantErr error
		anyErr  bool
	}{
		{name: "ok"},
		{name: "refuse overwrite", twice: true, wantErr: schemas.ErrExists},
		{name: "bad exp id", mutate: func(m *schemas.Manifest) { m.ExpID = "../evil" }, anyErr: true},
		{name: "upper case id", mutate: func(m *schemas.Manifest) { m.ExpID = "E_x" }, anyErr: true},
		{name: "hyphen id", mutate: func(m *schemas.Manifest) { m.ExpID = "e-x" }, anyErr: true},
		{name: "bad slot id", mutate: func(m *schemas.Manifest) { m.Slots[0].SlotID = "s/1" }, anyErr: true},
		{name: "duplicate slot", mutate: func(m *schemas.Manifest) { m.Slots[1].SlotID = "s_a1" }, anyErr: true},
		{name: "slot of other exp", mutate: func(m *schemas.Manifest) { m.Slots[0].ExpID = "e_other" }, anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t, nil)
			m := testManifest("e_test")
			if tc.mutate != nil {
				tc.mutate(m)
			}
			err := s.CreateExperiment(ctx, m)
			if tc.twice {
				if err != nil {
					t.Fatalf("first create: %v", err)
				}
				err = s.CreateExperiment(ctx, m)
			}
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.anyErr:
				if err == nil {
					t.Fatal("want error")
				}
				return
			case err != nil:
				t.Fatalf("create: %v", err)
			}
			got, err := s.LoadManifest(ctx, "e_test")
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
			if got.Hash != m.Hash || len(got.Slots) != 3 {
				t.Fatalf("manifest round trip: %+v", got)
			}
			var sl schemas.Slot
			if err := readJSON(filepath.Join(s.Root(), "e_test", "slots", "s_b1", "slot.json"), &sl); err != nil {
				t.Fatalf("slot.json: %v", err)
			}
			if sl.ExpID != "e_test" || sl.Arm != "b" {
				t.Fatalf("slot.json = %+v", sl)
			}
		})
	}
}

func TestListExperiments(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	mustCreateExp(t, s, "e_two")
	mustCreateExp(t, s, "e_one")
	// An interrupted create (no manifest) and a stray folder are not listed.
	if err := os.MkdirAll(filepath.Join(s.Root(), "e_partial", "slots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root(), ".tmp-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListExperiments(ctx)
	if err != nil {
		t.Fatalf("ListExperiments: %v", err)
	}
	if strings.Join(ids, ",") != "e_one,e_two" {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := s.LoadManifest(ctx, "e_missing"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("missing manifest err = %v", err)
	}
}

func TestNewRun(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	sl := slotOf(m, "s_a1")
	tests := []struct {
		name    string
		slot    *schemas.Slot
		run     schemas.Run
		wantErr error
		anyErr  bool
	}{
		{name: "ok, ids filled from slot", slot: sl, run: schemas.Run{RunID: "r_1", Attempt: 1}},
		{name: "refuse existing", slot: sl, run: schemas.Run{RunID: "r_1", Attempt: 1}, wantErr: schemas.ErrExists},
		{name: "slot mismatch", slot: sl, run: schemas.Run{RunID: "r_2", SlotID: "s_b1"}, anyErr: true},
		{name: "unknown slot", slot: &schemas.Slot{SlotID: "s_nope", ExpID: "e_test"}, run: schemas.Run{RunID: "r_3"}, wantErr: schemas.ErrNotFound},
		{name: "bad run id", slot: sl, run: schemas.Run{RunID: "r_../x"}, anyErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := tc.run
			dir, err := s.NewRun(ctx, tc.slot, &run)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.anyErr:
				if err == nil {
					t.Fatal("want error")
				}
				return
			case err != nil:
				t.Fatalf("NewRun: %v", err)
			}
			want := filepath.Join(s.Root(), "e_test", "slots", "s_a1", "runs", run.RunID)
			if dir != want {
				t.Fatalf("dir = %s, want %s", dir, want)
			}
			var got schemas.Run
			if err := readJSON(filepath.Join(dir, "run.json"), &got); err != nil {
				t.Fatalf("run.json: %v", err)
			}
			if got.ExpID != "e_test" || got.SlotID != "s_a1" || got.PairID != "p_1" {
				t.Fatalf("run.json ids = %+v", got)
			}
			if run.ExpID != "" {
				t.Fatal("NewRun modified the caller's run")
			}
		})
	}
}

func TestWriteRunFile(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r := mustNewRun(t, s, m, "s_a1", "r_1", 1, schemas.OutcomePass)
	tests := []struct {
		name    string
		file    string
		wantErr bool
	}{
		{name: "plain", file: "events.jsonl"},
		{name: "nested", file: "evidence/shim.log"},
		{name: "overwrite", file: "events.jsonl"},
		{name: "parent", file: "../escape", wantErr: true},
		{name: "inner parent", file: "a/../b", wantErr: true},
		{name: "absolute", file: "/etc/passwd", wantErr: true},
		{name: "dot", file: "./a", wantErr: true},
		{name: "double slash", file: "a//b", wantErr: true},
		{name: "backslash", file: `a\b`, wantErr: true},
		{name: "empty", file: "", wantErr: true},
		{name: "reserved seal", file: "seal.json", wantErr: true},
		{name: "reserved transitions", file: "transitions.jsonl", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := "data for " + tc.name
			err := s.WriteRunFile(ctx, r, tc.file, []byte(data))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q", tc.file)
				}
				return
			}
			if err != nil {
				t.Fatalf("WriteRunFile: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(s.runDir(r), filepath.FromSlash(tc.file)))
			if err != nil || string(got) != data {
				t.Fatalf("read back %q, %v", got, err)
			}
		})
	}
	entries, err := os.ReadDir(s.runDir(r))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
	t.Run("unknown run", func(t *testing.T) {
		other := *r
		other.RunID = "r_none"
		if err := s.WriteRunFile(ctx, &other, "x.json", nil); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("after seal", func(t *testing.T) {
		mustSeal(t, s, r)
		if err := s.WriteRunFile(ctx, r, "late.json", []byte("{}")); !errors.Is(err, schemas.ErrSealed) {
			t.Fatalf("err = %v, want ErrSealed", err)
		}
	})
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		id string
		ok bool
	}{
		{"e_codewins", true},
		{"r_p20fj3s8wa", true},
		{"abc123", true},
		{"", false},
		{"E_x", false},
		{"e-x", false},
		{"e.x", false},
		{"..", false},
		{"e/x", false},
		{strings.Repeat("a", 129), false},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			if err := validateID("id", tc.id); (err == nil) != tc.ok {
				t.Fatalf("validateID(%q) = %v, want ok=%v", tc.id, err, tc.ok)
			}
		})
	}
}
