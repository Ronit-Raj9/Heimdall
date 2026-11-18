package evidencestore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

func TestArchiveAndRestoreInPlace(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "org_a")
	f := newExperiment(t, org, "e_arch")
	f.sealed("s_1", "r_1")
	f.sealed("s_2", "r_2")
	f.analysis("a_1")
	before := treeFiles(t, f.dir())
	mir, _ := newDirMirror(t)
	stub, err := mir.Archive(ctx, org, "e_arch")
	if err != nil {
		t.Fatal(err)
	}
	if stub.Runs != 2 || stub.Name != "fixture" || stub.ListingSHA256 == "" || strings.Join(stub.Arms, ",") != "A,B" {
		t.Fatalf("stub: %+v", stub)
	}
	entries, _ := os.ReadDir(f.dir())
	if len(entries) != 1 || entries[0].Name() != stubFile {
		t.Fatalf("archived folder holds %v", entries)
	}
	ids, _ := org.Store.ListExperiments(ctx)
	if len(ids) != 0 {
		t.Fatalf("store still lists %v", ids)
	}
	stubs, err := ListArchived(org.root())
	if err != nil || len(stubs) != 1 || stubs[0].ExpID != "e_arch" {
		t.Fatalf("ListArchived: %v %v", stubs, err)
	}
	// A new experiment can not take the archived id.
	if err := org.Store.CreateExperiment(ctx, &schemas.Manifest{ExpID: "e_arch"}); !errors.Is(err, schemas.ErrExists) {
		t.Fatalf("id reuse: %v", err)
	}
	// Mirroring an archived experiment is not an error for the sweep: it is
	// not listed. Directly it says so.
	if _, err := mir.Experiment(ctx, org, "e_arch"); !errors.Is(err, ErrArchived) {
		t.Fatalf("mirror archived: %v", err)
	}
	if rep, err := mir.Verify(ctx, org, "e_arch", true); err != nil || !rep.OK() || !rep.Archived {
		t.Fatalf("verify archived: %+v %v", rep, err)
	}
	rr, err := mir.Restore(ctx, org, "e_arch", RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rr.ReplacedStub {
		t.Fatal("stub not replaced")
	}
	after := treeFiles(t, f.dir())
	for p, sum := range before {
		if strings.HasSuffix(p, "lease.lock") {
			continue
		}
		if after[p] != sum {
			t.Errorf("%s differs after archive and restore", p)
		}
	}
	if _, ok := after[stubFile]; ok {
		t.Fatal("stub left in restored folder")
	}
	ids, _ = org.Store.ListExperiments(ctx)
	if len(ids) != 1 {
		t.Fatalf("restored experiment not listed: %v", ids)
	}
	fresh, _ := store.NewFSStore(org.root(), nil)
	if err := fresh.VerifyChain("e_arch"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(org.root(), stateDir, "trash")); len(entries) != 0 {
		t.Fatalf("trash left: %v", entries)
	}
}

func TestArchiveRefusals(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_unsealed")
	mir, d := newDirMirror(t)
	if _, err := mir.Archive(ctx, org, "e_unsealed"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not sealed") {
		t.Fatalf("unsealed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir(), manifestFile)); err != nil {
		t.Fatal("refused archive touched the folder")
	}
	// A mirror that loses an object fails the verification: nothing is
	// deleted.
	f2 := newExperiment(t, org, "e_lost")
	f2.sealed("s_1", "r_1")
	if _, err := mir.Experiment(ctx, org, "e_lost"); err != nil {
		t.Fatal(err)
	}
	gone := blobDropping{Blob: d, drop: "r_1/events.jsonl"}
	if _, err := NewMirror(gone, nil).Archive(ctx, org, "e_lost"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("lost object: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f2.dir(), manifestFile)); err != nil {
		t.Fatal("refused archive deleted the folder")
	}
}

// blobDropping hides one object, as if the bucket lost it.
type blobDropping struct {
	Blob
	drop string
}

func (b blobDropping) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	all, err := b.Blob.List(ctx, prefix)
	out := all[:0]
	for _, o := range all {
		if !strings.HasSuffix(o.Key, b.drop) {
			out = append(out, o)
		}
	}
	return out, err
}

func (b blobDropping) Head(ctx context.Context, key string) (ObjectInfo, error) {
	if strings.HasSuffix(key, b.drop) {
		return ObjectInfo{}, ErrNotFound
	}
	return b.Blob.Head(ctx, key)
}

func (b blobDropping) Put(ctx context.Context, key string, in PutInput) error {
	if strings.HasSuffix(key, b.drop) {
		return nil // accepted and lost
	}
	return b.Blob.Put(ctx, key, in)
}

func TestApplyRetention(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	old := newExperiment(t, org, "e_old")
	old.sealed("s_1", "r_1")
	recent := newExperiment(t, org, "e_recent")
	recent.sealed("s_1", "r_1b")
	mir, _ := newDirMirror(t)
	mir.Now = func() time.Time { return time.Now().Add(40 * 24 * time.Hour) }
	// Make e_recent active "now" (40 days ahead) by touching its seals.log.
	future := time.Now().Add(39 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(recent.dir(), sealsLogFile), future, future); err != nil {
		t.Fatal(err)
	}
	items, err := mir.ApplyRetention(ctx, org, RetentionOptions{Days: 30, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.ExpID] = it.Action
	}
	if got["e_old"] != "would_archive" || got["e_recent"] != "kept" {
		t.Fatalf("dry run: %v", got)
	}
	if _, err := os.Stat(filepath.Join(old.dir(), manifestFile)); err != nil {
		t.Fatal("dry run archived")
	}
	items, err = mir.ApplyRetention(ctx, org, RetentionOptions{Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		got[it.ExpID] = it.Action
	}
	if got["e_old"] != "archived" || got["e_recent"] != "kept" {
		t.Fatalf("retention: %v", items)
	}
	if s, err := ReadStub(org.root(), "e_old"); err != nil || s.Runs != 1 {
		t.Fatalf("stub: %+v %v", s, err)
	}
	if items, _ := mir.ApplyRetention(ctx, org, RetentionOptions{Days: 0}); len(items) != 0 {
		t.Fatal("days 0 archived something")
	}
}

type fakeSettings map[string]string

func (f fakeSettings) GetSetting(_ context.Context, orgID, key string) (string, bool, error) {
	v, ok := f[orgID+"/"+key]
	return v, ok, nil
}

func TestRetentionDays(t *testing.T) {
	ctx := context.Background()
	s := fakeSettings{"org_a/" + RetentionSettingKey: "30", "org_b/" + RetentionSettingKey: "soon"}
	if d, err := RetentionDays(ctx, s, "org_a"); err != nil || d != 30 {
		t.Fatalf("org_a: %d %v", d, err)
	}
	if d, err := RetentionDays(ctx, s, "org_c"); err != nil || d != 0 {
		t.Fatalf("org_c: %d %v", d, err)
	}
	if _, err := RetentionDays(ctx, s, "org_b"); err == nil {
		t.Fatal("org_b: want error")
	}
}

// hookBlob runs fn on the n-th Get of keys with the suffix.
type hookBlob struct {
	Blob
	suffix string
	n      int
	gets   int
	fn     func()
}

func (b *hookBlob) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if strings.HasSuffix(key, b.suffix) {
		b.gets++
		if b.gets == b.n {
			b.fn()
		}
	}
	return b.Blob.Get(ctx, key)
}

func TestArchiveGuards(t *testing.T) {
	ctx := context.Background()
	t.Run("live lease", func(t *testing.T) {
		org := newOrg(t, "local")
		f := newExperiment(t, org, "e_lease")
		f.sealed("s_1", "r_1")
		lease := filepath.Join(f.dir(), "slots", "s_2", "lease.json")
		mir, _ := newDirMirror(t)
		write := func(at time.Time) {
			b, _ := json.Marshal(schemas.Lease{SlotID: "s_2", Owner: "w1", HeartbeatAt: at})
			if err := os.WriteFile(lease, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write(time.Now())
		if _, err := mir.Archive(ctx, org, "e_lease"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "live lease") {
			t.Fatalf("live lease: %v", err)
		}
		write(time.Now().Add(-3 * time.Hour)) // left by a crash
		if _, err := mir.Archive(ctx, org, "e_lease"); err != nil {
			t.Fatalf("stale lease: %v", err)
		}
	})
	t.Run("entry that would not be mirrored", func(t *testing.T) {
		org := newOrg(t, "local")
		f := newExperiment(t, org, "e_dotted")
		f.sealed("s_1", "r_1")
		if err := os.WriteFile(filepath.Join(f.dir(), ".notes"), []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
		mir, _ := newDirMirror(t)
		if _, err := mir.Archive(ctx, org, "e_dotted"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), ".notes") {
			t.Fatalf("dot file: %v", err)
		}
		if !isLive(f.dir()) {
			t.Fatal("folder deleted")
		}
	})
	t.Run("write after the mirror pass", func(t *testing.T) {
		org := newOrg(t, "local")
		f := newExperiment(t, org, "e_late")
		f.sealed("s_1", "r_1")
		_, d := newDirMirror(t)
		// Listing reads: mirror pass (1), verify (2), restore drill (3).
		// An analysis lands during the drill, after every check read the
		// folder.
		hb := &hookBlob{Blob: d, suffix: "/" + listingName, n: 3, fn: func() { f.analysis("a_late") }}
		if _, err := NewMirror(hb, nil).Archive(ctx, org, "e_late"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "a_late") {
			t.Fatalf("late write: %v", err)
		}
		if _, err := os.Stat(filepath.Join(f.dir(), "analysis", "a_late", "verdict.json")); err != nil {
			t.Fatal("late analysis lost")
		}
		if !isLive(f.dir()) {
			t.Fatal("folder not put back")
		}
		// The next attempt mirrors the analysis and archives.
		if _, err := NewMirror(d, nil).Archive(ctx, org, "e_late"); err != nil {
			t.Fatal(err)
		}
	})
}
