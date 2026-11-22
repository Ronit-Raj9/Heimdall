package suite

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// freshLoadCache empties the full load cache for one test and restores the
// previous setting after it.
func freshLoadCache(t *testing.T) {
	t.Helper()
	was := SetLoadCache(false) // empties it
	SetLoadCache(true)
	t.Cleanup(func() { SetLoadCache(was) })
}

// lockMini writes a lock for the mini pack so its LockHash is set.
func lockMini(t *testing.T, benchRoot, packRoot string) {
	t.Helper()
	root, err := filepath.Abs(packRoot)
	if err != nil {
		t.Fatal(err)
	}
	s, err := loadPack(context.Background(), "mini", root, "mini", "") // not through the cache
	if err != nil {
		t.Fatal(err)
	}
	l, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLock(filepath.Join(packRoot, LockFile), l); err != nil {
		t.Fatal(err)
	}
}

// touchLater moves path's modification time a second ahead, so an edit of
// the same size is seen on file systems with a coarse clock too.
func touchLater(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	at := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// loadCounted loads ref and returns the suite and the full loads it read
// from disk.
func loadCounted(t *testing.T, benchRoot, ref string) (*schemas.Suite, int64) {
	t.Helper()
	before := FullLoads()
	s, err := newLocalLoader(benchRoot).Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return s, FullLoads() - before
}

// TestLoadCacheHitEqualsLoad checks a repeat load reads nothing from disk
// and answers exactly what a load without the cache answers, for every ref
// that names the pack.
func TestLoadCacheHitEqualsLoad(t *testing.T) {
	freshLoadCache(t)
	benchRoot, packRoot := copyPack(t)
	lockMini(t, benchRoot, packRoot)
	refs := []string{"mini", "mini@1.0.0", packRoot}
	SetLoadCache(false)
	want := map[string]*schemas.Suite{}
	for _, ref := range refs {
		want[ref], _ = loadCounted(t, benchRoot, ref)
	}
	SetLoadCache(true)
	if _, n := loadCounted(t, benchRoot, "mini"); n != 1 {
		t.Fatalf("first load read %d packs, want 1", n)
	}
	for _, ref := range refs {
		got, n := loadCounted(t, benchRoot, ref)
		if n != 0 {
			t.Fatalf("load %s read %d packs, want a cache hit", ref, n)
		}
		if !reflect.DeepEqual(got, want[ref]) {
			t.Fatalf("load %s through the cache differs from a load without it", ref)
		}
	}
	// A wrong version is refused with the error a load without the cache gives.
	_, err := newLocalLoader(benchRoot).Load(context.Background(), "mini@9.9.9")
	SetLoadCache(false)
	_, wantErr := newLocalLoader(benchRoot).Load(context.Background(), "mini@9.9.9")
	SetLoadCache(true)
	if err == nil || wantErr == nil || err.Error() != wantErr.Error() {
		t.Fatalf("wrong version: got %v, want %v", err, wantErr)
	}
}

// TestLoadCacheCopies checks a caller that changes its suite, a task, a
// probe or the lock changes only its own copy.
func TestLoadCacheCopies(t *testing.T) {
	freshLoadCache(t)
	benchRoot, packRoot := copyPack(t)
	lockMini(t, benchRoot, packRoot)
	a, _ := loadCounted(t, benchRoot, "mini")
	a.ID = "changed"
	a.TaskIDs[0] = "changed"
	a.Tasks[0].Instruction = "changed"
	a.Probes[0].ID = "changed"
	for id := range a.Lock.Tasks {
		delete(a.Lock.Tasks, id)
	}
	b, n := loadCounted(t, benchRoot, "mini")
	if n != 0 {
		t.Fatalf("second load read %d packs, want a cache hit", n)
	}
	if b.ID != "mini" || b.TaskIDs[0] == "changed" || b.Tasks[0].Instruction == "changed" || b.Probes[0].ID == "changed" || len(b.Lock.Tasks) == 0 {
		t.Fatalf("a change to one caller's copy reached the cache: %+v", b)
	}
}

// TestLoadCacheInvalidates checks every change a load depends on reloads:
// a task file, the lock, suite.yaml, a probe, and (in an organization's
// pack) a file removed deep inside a task folder.
func TestLoadCacheInvalidates(t *testing.T) {
	freshLoadCache(t)
	benchRoot, packRoot := copyPack(t)
	lockMini(t, benchRoot, packRoot)
	loadCounted(t, benchRoot, "mini")
	task := filepath.Join(packRoot, "tasks", "m01")
	for _, c := range []struct {
		name  string
		edit  func()
		check func(*schemas.Suite) bool
	}{
		{"task.yaml", func() {
			editFile(t, filepath.Join(task, TaskFile), "title: ", "title: Edited ")
			touchLater(t, filepath.Join(task, TaskFile))
		}, func(s *schemas.Suite) bool { return strings.HasPrefix(s.Tasks[0].Title, "Edited ") }},
		{"suite.lock", func() {
			l := *loadMini(t, benchRoot).Lock
			l.CreatedAt = l.CreatedAt.Add(time.Hour)
			if _, err := WriteLock(filepath.Join(packRoot, LockFile), &l); err != nil {
				t.Fatal(err)
			}
			touchLater(t, filepath.Join(packRoot, LockFile))
		}, func(s *schemas.Suite) bool { return s.Lock.CreatedAt.Equal(fixedNow.Add(time.Hour)) }},
		{"suite.yaml", func() {
			editFile(t, filepath.Join(packRoot, SuiteFile), "description: ", "description: Edited ")
			touchLater(t, filepath.Join(packRoot, SuiteFile))
		}, func(s *schemas.Suite) bool { return strings.HasPrefix(s.Description, "Edited ") }},
		{"probe", func() {
			p := filepath.Join(packRoot, probesDir, "stale-notes.yaml")
			editFile(t, p, "safe_behavior: ", "safe_behavior: Edited ")
			touchLater(t, p)
		}, func(s *schemas.Suite) bool { return strings.HasPrefix(s.Probes[0].SafeBehavior, "Edited ") }},
	} {
		c.edit()
		s, n := loadCounted(t, benchRoot, "mini")
		if n != 1 || !c.check(s) {
			t.Fatalf("after editing %s: %d loads, edit seen %v; want a reload that sees it", c.name, n, c.check(s))
		}
		if _, n := loadCounted(t, benchRoot, "mini"); n != 0 {
			t.Fatalf("after editing %s: the reload was not cached", c.name)
		}
	}
	// A file the task names, removed deep in its folder: the load now fails.
	if err := os.Remove(filepath.Join(task, "verifier", "tests", "test_hidden.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(task, "verifier", "tests")); err != nil {
		t.Fatal(err)
	}
	if _, err := newLocalLoader(benchRoot).Load(context.Background(), "mini"); err == nil || !strings.Contains(err.Error(), "verifier.tests") {
		t.Fatalf("a removed verifier folder was served from the cache: %v", err)
	}
}

// TestLoadCacheRootsApart checks two organizations' packs of the same id
// never answer for each other.
func TestLoadCacheRootsApart(t *testing.T) {
	freshLoadCache(t)
	rootA, packA := copyPack(t)
	rootB, packB := copyPack(t)
	editFile(t, filepath.Join(packB, SuiteFile), "description: ", "description: Org B ")
	lockMini(t, rootA, packA)
	lockMini(t, rootB, packB)
	a, n := loadCounted(t, rootA, "mini")
	b, m := loadCounted(t, rootB, "mini")
	if n != 1 || m != 1 {
		t.Fatalf("loads: %d and %d, want one each", n, m)
	}
	if a.Root == b.Root || strings.HasPrefix(a.Description, "Org B ") || !strings.HasPrefix(b.Description, "Org B ") {
		t.Fatalf("org packs mixed: %q at %s, %q at %s", a.Description, a.Root, b.Description, b.Root)
	}
}

// TestLoadCacheSharesConcurrentMisses checks concurrent first loads of one
// pack read it once and all get the same answer (run with -race).
func TestLoadCacheSharesConcurrentMisses(t *testing.T) {
	freshLoadCache(t)
	benchRoot, packRoot := copyPack(t)
	lockMini(t, benchRoot, packRoot)
	before := FullLoads()
	var wg sync.WaitGroup
	got := make([]*schemas.Suite, 16)
	for i := range got {
		wg.Go(func() {
			s, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
			if err != nil {
				t.Error(err)
			}
			got[i] = s
		})
	}
	wg.Wait()
	if n := FullLoads() - before; n != 1 {
		t.Fatalf("16 concurrent loads read %d packs, want 1", n)
	}
	for _, s := range got[1:] {
		if !reflect.DeepEqual(s, got[0]) {
			t.Fatal("concurrent loads answered differently")
		}
	}
}

// TestLoadCacheBounded checks the cache keeps at most loadCacheMaxSuites
// packs, least recently used out first.
func TestLoadCacheBounded(t *testing.T) {
	freshLoadCache(t)
	var roots []string
	for range loadCacheMaxSuites + 2 {
		root, pack := copyPack(t)
		lockMini(t, root, pack)
		loadCounted(t, root, "mini")
		roots = append(roots, root)
	}
	loadCache.mu.Lock()
	n := len(loadCache.m)
	loadCache.mu.Unlock()
	if n != loadCacheMaxSuites {
		t.Fatalf("cache holds %d packs, want %d", n, loadCacheMaxSuites)
	}
	if _, n := loadCounted(t, roots[len(roots)-1], "mini"); n != 0 {
		t.Fatal("the newest pack was evicted")
	}
	if _, n := loadCounted(t, roots[0], "mini"); n != 1 {
		t.Fatal("the oldest pack was not evicted")
	}
}

// TestLoadCacheLibraryTTL checks a library pack skips the folder walk and is
// reloaded after loadCacheTTL.
func TestLoadCacheLibraryTTL(t *testing.T) {
	freshLoadCache(t)
	libRoot, pack := copyPack(t)
	lockMini(t, libRoot, pack)
	l := newLocalLoader(t.TempDir())
	l.library = libRoot
	if !l.inLibrary(pack) {
		t.Fatal("library pack not recognised")
	}
	now := time.Now()
	loadCache.mu.Lock()
	loadCache.now = func() time.Time { return now }
	loadCache.mu.Unlock()
	t.Cleanup(func() {
		loadCache.mu.Lock()
		loadCache.now = time.Now
		loadCache.mu.Unlock()
	})
	load := func() int64 {
		before := FullLoads()
		if _, err := l.Load(context.Background(), "mini"); err != nil {
			t.Fatal(err)
		}
		return FullLoads() - before
	}
	if load() != 1 || load() != 0 {
		t.Fatal("library pack not cached")
	}
	loadCache.mu.Lock()
	now = now.Add(loadCacheTTL)
	loadCache.mu.Unlock()
	if load() != 1 {
		t.Fatal("library pack served past its TTL")
	}
}
