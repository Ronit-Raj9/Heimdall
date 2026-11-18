package evidencestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

type fixture struct {
	t    *testing.T
	org  Org
	m    *schemas.Manifest
	runs map[string]*schemas.Run
}

// newOrg returns an org over a fresh store root. Sealed folders are 0555, so
// a cleanup makes the tree writable for TempDir's removal.
func newOrg(t *testing.T, id string) Org {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Org{ID: id, Store: st}
}

func makeWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o644)
	})
}

// newExperiment creates an experiment with four slots and a real manifest
// hash.
func newExperiment(t *testing.T, org Org, expID string) *fixture {
	t.Helper()
	m := &schemas.Manifest{
		ExpID:      expID,
		CreatedAt:  time.Now().UTC().Add(-time.Hour),
		Experiment: schemas.Experiment{ID: expID, Name: "fixture", Suite: "coding-core@1.0.0"},
		SuiteID:    "coding_core",
		Arms:       map[string]schemas.ArmManifest{"A": {}, "B": {}},
	}
	for i := 1; i <= 4; i++ {
		arm := "A"
		if i%2 == 0 {
			arm = "B"
		}
		m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%d", i), ExpID: expID, PairID: fmt.Sprintf("p_%d", (i+1)/2),
			Task: "t01", Arm: arm, Condition: "clean", Trial: (i + 1) / 2, OrderIndex: i, Counted: true})
	}
	h, err := engine.ManifestHash(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Hash = h
	if err := org.Store.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, org: org, m: m, runs: map[string]*schemas.Run{}}
}

func (f *fixture) slot(id string) *schemas.Slot {
	for i := range f.m.Slots {
		if f.m.Slots[i].SlotID == id {
			s := f.m.Slots[i]
			return &s
		}
	}
	f.t.Fatalf("no slot %s", id)
	return nil
}

// run creates a run with a few files; seal it with seal().
func (f *fixture) run(slotID, runID string, extra ...string) *schemas.Run {
	f.t.Helper()
	sl := f.slot(slotID)
	r := &schemas.Run{RunID: runID, SlotID: slotID, PairID: sl.PairID, ExpID: f.m.ExpID, Attempt: 1,
		Stage: schemas.StageCleanup, ManifestHash: f.m.Hash, Outcome: schemas.OutcomePass}
	ctx := context.Background()
	if _, err := f.org.Store.NewRun(ctx, sl, r); err != nil {
		f.t.Fatal(err)
	}
	files := map[string]string{
		"events.jsonl":      `{"seq":1}` + "\n",
		"workspace.diff":    "--- a\n+++ b\n",
		"evidence/shim.log": "shim " + runID + "\n",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		files[extra[i]] = extra[i+1]
	}
	for name, data := range files {
		if err := f.org.Store.WriteRunFile(ctx, r, name, []byte(data)); err != nil {
			f.t.Fatal(err)
		}
	}
	f.runs[runID] = r
	return r
}

func (f *fixture) seal(runID string) {
	f.t.Helper()
	if _, err := f.org.Store.Seal(context.Background(), f.runs[runID]); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) sealed(slotID, runID string, extra ...string) {
	f.t.Helper()
	f.run(slotID, runID, extra...)
	f.seal(runID)
}

func (f *fixture) analysis(id string) {
	f.t.Helper()
	a := &schemas.Analysis{AnalysisID: id, ExpID: f.m.ExpID, CreatedAt: time.Now().UTC()}
	if err := f.org.Store.WriteAnalysis(context.Background(), a, map[string][]byte{"report.md": []byte("# report " + id + "\n")}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) dir() string { return f.org.expDir(f.m.ExpID) }

// runDir returns the folder of a run.
func (f *fixture) runDir(runID string) string {
	r := f.runs[runID]
	return filepath.Join(f.dir(), "slots", r.SlotID, "runs", runID)
}

// tamper rewrites a file inside a read-only sealed folder.
func tamper(t *testing.T, p string, data []byte) {
	t.Helper()
	dir := filepath.Dir(p)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0o644)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newDirMirror(t *testing.T) (*Mirror, *Dir) {
	t.Helper()
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewMirror(d, nil), d
}

// standard builds the usual experiment: three sealed runs, one unsealed run,
// one analysis.
func standard(t *testing.T, org Org, expID string) *fixture {
	f := newExperiment(t, org, expID)
	f.sealed("s_1", "r_1")
	f.sealed("s_2", "r_2", "big.bin", strings.Repeat("x", 300_000))
	f.sealed("s_3", "r_3")
	f.run("s_4", "r_4") // in flight
	f.analysis("a_1")
	return f
}

// treeFiles returns relative path -> sha256 of every regular file under
// dir, skipping names that start with a dot.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && p != dir {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		sum, err := utils.SHA256File(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sum
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMirrorAndRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "org_test")
	f := standard(t, org, "e_round")
	mir, d := newDirMirror(t)
	res, err := mir.Experiment(ctx, org, "e_round")
	if err != nil {
		t.Fatal(err)
	}
	if res.Seals != 3 || len(res.Pending) != 1 || res.Pending[0] != "r_4" || res.Uploaded != res.Files || res.Generation != 1 {
		t.Fatalf("result: %+v", res)
	}
	// The mirror holds the experiment's immutable files and nothing else.
	local := treeFiles(t, f.dir())
	mirrored := treeFiles(t, filepath.Join(d.Root(), "org_test", "e_round"))
	for p, sum := range mirrored {
		if local[p] != sum {
			t.Errorf("mirrored %s differs from local", p)
		}
	}
	for p := range local {
		_, in := mirrored[p]
		want := !strings.Contains(p, "/r_4/") && !strings.HasSuffix(p, "lease.lock") && !strings.HasSuffix(p, "lease.json")
		if in != want {
			t.Errorf("%s mirrored=%v, want %v", p, in, want)
		}
	}
	if _, err := os.Stat(filepath.Join(d.Root(), "org_test", "e_round", listingName)); err != nil {
		t.Fatal("no listing")
	}
	// Restore into a separate folder (an auditor's copy).
	target := t.TempDir()
	t.Cleanup(func() { makeWritable(target) })
	rr, err := mir.Restore(ctx, org, "e_round", RestoreOptions{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if rr.Files != res.Files || rr.Seals != 3 || rr.ListingSHA256 != res.ListingSHA256 {
		t.Fatalf("restore: %+v", rr)
	}
	restored := treeFiles(t, filepath.Join(target, "e_round"))
	if fmt.Sprint(sortedMap(restored)) != fmt.Sprint(sortedMap(mirrored)) {
		t.Fatal("restored tree differs from the mirror")
	}
	st, _ := store.NewFSStore(target, nil)
	if err := st.VerifyChain("e_round"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"r_1", "r_2", "r_3"} {
		if err := st.Verify(ctx, "e_round", id); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(filepath.Join(target, "e_round", "slots", f.runs[id].SlotID, "runs", id, "seal.json"))
		if info.Mode().Perm() != 0o444 {
			t.Fatalf("restored run %s not read-only: %v", id, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(target, stateDir, "staging")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(target, stateDir, "staging"))
		if len(entries) != 0 {
			t.Fatalf("staging not cleaned: %v", entries)
		}
	}
	// A live experiment folder is never replaced.
	if _, err := mir.Restore(ctx, org, "e_round", RestoreOptions{}); !errors.Is(err, schemas.ErrExists) {
		t.Fatalf("restore over a live folder: %v", err)
	}
}

func sortedMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func TestMirrorIdempotentAndIncremental(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_inc")
	mir, _ := newDirMirror(t)
	first, err := mir.Experiment(ctx, org, "e_inc")
	if err != nil {
		t.Fatal(err)
	}
	again, err := mir.Experiment(ctx, org, "e_inc")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Unchanged || again.Uploaded != 0 || again.Resumed != 0 || again.Generation != first.Generation || again.ListingSHA256 != first.ListingSHA256 {
		t.Fatalf("second pass: %+v", again)
	}
	// The in-flight run seals, a review-like child artifact appears.
	f.seal("r_4")
	f.analysis("a_2")
	third, err := mir.Experiment(ctx, org, "e_inc")
	if err != nil {
		t.Fatal(err)
	}
	// r_4's files (5: run.json, three written, seal.json, transitions?) +
	// a_2's two files + seals.log; nothing already mirrored is resent.
	if third.Generation != 2 || third.Seals != 4 || len(third.Pending) != 0 {
		t.Fatalf("third pass: %+v", third)
	}
	newFiles := 0
	for p := range treeFiles(t, f.runDir("r_4")) {
		_ = p
		newFiles++
	}
	if want := newFiles + 2 + 1; third.Uploaded != want {
		t.Fatalf("third pass uploaded %d, want %d", third.Uploaded, want)
	}
}

func TestMirrorRefusesTamperedRun(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_tamper")
	mir, d := newDirMirror(t)
	if _, err := mir.Experiment(ctx, org, "e_tamper"); err != nil {
		t.Fatal(err)
	}
	listing := filepath.Join(d.Root(), "local", "e_tamper", listingName)
	before, _ := os.ReadFile(listing)
	tamper(t, filepath.Join(f.runDir("r_2"), "workspace.diff"), []byte("--- a\n+++ b\n+evil\n"))
	f.sealed("s_4", "r_4b")
	_, err := mir.Experiment(ctx, org, "e_tamper")
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "workspace.diff") {
		t.Fatalf("tampered run: %v", err)
	}
	after, _ := os.ReadFile(listing)
	if !bytes.Equal(before, after) {
		t.Fatal("listing changed after a refused pass")
	}
	if _, err := os.Stat(filepath.Join(d.Root(), "local", "e_tamper", "slots", "s_4", "runs")); err == nil {
		t.Fatal("refused pass sent files")
	}
}

func TestCheckAgainstListing(t *testing.T) {
	log1 := []byte("r_1 " + strings.Repeat("a", 64) + "\n")
	log2 := append(append([]byte{}, log1...), []byte("r_2 "+strings.Repeat("b", 64)+"\n")...)
	prev := &Listing{ManifestHash: "m", Files: []FileEntry{
		{Path: "manifest.json", Size: 3, SHA256: utils.SHA256Bytes([]byte("abc"))},
		{Path: sealsLogFile, Size: int64(len(log1)), SHA256: utils.SHA256Bytes(log1)},
	}}
	snap := func(manifest string, log []byte) *snapshot {
		return &snapshot{ManifestHash: "m", Files: []localFile{
			{Path: "manifest.json", Size: int64(len(manifest)), SHA256: utils.SHA256Bytes([]byte(manifest))},
			logFile(sealsLogFile, log),
		}}
	}
	if err := checkAgainstListing(snap("abc", log2), prev); err != nil {
		t.Fatalf("grown log: %v", err)
	}
	if err := checkAgainstListing(snap("abc", log1), prev); err != nil {
		t.Fatalf("same log: %v", err)
	}
	rewritten := []byte("r_1 " + strings.Repeat("c", 64) + "\n" + "r_2 " + strings.Repeat("b", 64) + "\n")
	if err := checkAgainstListing(snap("abc", rewritten), prev); err == nil || !strings.Contains(err.Error(), "does not extend") {
		t.Fatalf("rewritten log: %v", err)
	}
	if err := checkAgainstListing(snap("abc", log1[:10]), prev); err == nil {
		t.Fatal("shortened log accepted")
	}
	if err := checkAgainstListing(snap("abd", log2), prev); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("changed file: %v", err)
	}
	s := snap("abc", log2)
	s.ManifestHash = "other"
	if err := checkAgainstListing(s, prev); err == nil {
		t.Fatal("changed manifest hash accepted")
	}
}

// failingBlob fails every Put after the first n.
type failingBlob struct {
	Blob
	n    int64
	puts atomic.Int64
}

func (b *failingBlob) Put(ctx context.Context, key string, in PutInput) error {
	if b.puts.Add(1) > b.n {
		return fmt.Errorf("injected network failure")
	}
	return b.Blob.Put(ctx, key, in)
}

func TestMirrorResumes(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	standard(t, org, "e_resume")
	_, d := newDirMirror(t)
	flaky := &failingBlob{Blob: d, n: 7}
	mir := NewMirror(flaky, nil)
	mir.Concurrency = 1
	if _, err := mir.Experiment(ctx, org, "e_resume"); err == nil {
		t.Fatal("want injected failure")
	}
	if _, _, err := readListing(ctx, d, "local", "e_resume"); !isNotFound(err) {
		t.Fatalf("interrupted pass wrote a listing: %v", err)
	}
	mir = NewMirror(d, nil)
	res, err := mir.Experiment(ctx, org, "e_resume")
	if err != nil {
		t.Fatal(err)
	}
	if res.Resumed != 7 || res.Uploaded != res.Files-7 {
		t.Fatalf("resume: %+v", res)
	}
}

func TestSweepAndDiscoverOrgs(t *testing.T) {
	ctx := context.Background()
	runs := t.TempDir()
	t.Cleanup(func() { makeWritable(runs) })
	localStore, _ := store.NewFSStore(runs, nil)
	newExperiment(t, Org{ID: "local", Store: localStore}, "e_local").sealed("s_1", "r_l")
	orgStore, _ := store.NewFSStore(filepath.Join(runs, "orgs", "org_x"), nil)
	newExperiment(t, Org{ID: "org_x", Store: orgStore}, "e_x").sealed("s_1", "r_x")
	orgs, err := DiscoverOrgs(runs, nil)
	if err != nil || len(orgs) != 2 || orgs[0].ID != "local" || orgs[1].ID != "org_x" {
		t.Fatalf("orgs: %v %v", orgs, err)
	}
	mir, d := newDirMirror(t)
	items, err := mir.Sweep(ctx, orgs)
	if err != nil || len(items) != 2 {
		t.Fatalf("sweep: %v %v", items, err)
	}
	for _, k := range []string{"local/e_local/" + listingName, "org_x/e_x/" + listingName} {
		if _, err := d.Head(ctx, k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	if _, err := FindOrg(runs, "org_missing", nil); err == nil {
		t.Fatal("missing org found")
	}
	if _, err := FindOrg(runs, "../x", nil); err == nil {
		t.Fatal("bad org id accepted")
	}
}

func TestConcurrentPassesSerialize(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	standard(t, org, "e_conc")
	mir, _ := newDirMirror(t)
	var wg sync.WaitGroup
	results := make([]*MirrorResult, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := mir.Experiment(ctx, org, "e_conc")
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}(i)
	}
	wg.Wait()
	sent := 0
	for _, r := range results {
		if r != nil {
			sent += r.Uploaded
		}
	}
	if sent != results[0].Files {
		t.Fatalf("files sent %d times across passes, want each once (%d)", sent, results[0].Files)
	}
}

func TestSweepContinuesPastFailingOrg(t *testing.T) {
	ctx := context.Background()
	bad := newOrg(t, "org_bad")
	good := newOrg(t, "org_good")
	newExperiment(t, good, "e_ok").sealed("s_1", "r_1")
	makeWritable(bad.root())
	if err := os.RemoveAll(bad.root()); err != nil {
		t.Fatal(err)
	}
	mir, d := newDirMirror(t)
	items, err := mir.Sweep(ctx, []Org{bad, good})
	if err == nil || len(items) != 2 || items[0].Error == "" || items[1].Error != "" {
		t.Fatalf("sweep: %+v %v", items, err)
	}
	if _, err := d.Head(ctx, listingKey("org_good", "e_ok")); err != nil {
		t.Fatal("org after the failing one not mirrored")
	}
}

func TestMirrorRepair(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	standard(t, org, "e_repair")
	mir, d := newDirMirror(t)
	if _, err := mir.Experiment(ctx, org, "e_repair"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(ctx, "local/e_repair/slots/s_1/runs/r_1/events.jsonl"); err != nil {
		t.Fatal(err)
	}
	res, err := mir.Experiment(ctx, org, "e_repair")
	if err != nil || !res.Unchanged || res.Repaired != 0 {
		t.Fatalf("plain pass trusts the listing: %+v %v", res, err)
	}
	if rep, _ := mir.Verify(ctx, org, "e_repair", false); rep.OK() {
		t.Fatal("verify missed the lost object")
	}
	res, err = mir.ExperimentWith(ctx, org, "e_repair", MirrorOptions{Repair: true})
	if err != nil || res.Repaired != 1 || res.Uploaded != 1 {
		t.Fatalf("repair: %+v %v", res, err)
	}
	if rep, err := mir.Verify(ctx, org, "e_repair", true); err != nil || !rep.OK() {
		t.Fatalf("verify after repair: %+v %v", rep, err)
	}
}
