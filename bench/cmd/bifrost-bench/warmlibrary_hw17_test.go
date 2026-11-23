package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The warm takes the library packs used in the last 14 days first (newest
// first, version suffix stripped, org packs and repeats left out), then the
// largest library packs by task folders, at most 15 in all.
func TestWarmPickOrder(t *testing.T) {
	library := []string{"a", "b", "big1", "big2", "big3", "big4", "big5", "big6", "c"}
	sizes := map[string]int{"a": 3, "b": 1, "big1": 900, "big2": 800, "big3": 700, "big4": 600, "big5": 500, "big6": 400, "c": 2}
	recent := []string{"c@1.0.0", "org-only@2.0.0", "a@1.0.0", "c@0.9.0", "big2@1.0.0"}
	got := warmPick(recent, library, sizes)
	want := []string{"c", "a", "big2", "big1", "big3", "big4", "big5", "big6"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warmPick = %v, want %v", got, want)
	}

	var many []string
	manySizes := map[string]int{}
	for i := range 30 {
		id := string(rune('a'+i%26)) + string(rune('a'+i/26))
		many = append(many, id)
		manySizes[id] = i
	}
	if got := warmPick(many, many, manySizes); len(got) != warmMax || got[0] != many[0] {
		t.Errorf("warmPick over 30 recent packs = %d packs starting %v, want %d starting %s", len(got), got, warmMax, many[0])
	}
}

// HEIMDALL_WARM_SUITES=off skips the warm.
func TestWarmLibraryOff(t *testing.T) {
	t.Setenv(warmSuitesEnv, "off")
	called := false
	prev := warmRun
	warmRun = func(ctx context.Context, h *handlers.SuitesHandler, ids []string, l schemas.Logger) <-chan struct{} {
		called = true
		return prev(ctx, h, ids, l)
	}
	t.Cleanup(func() { warmRun = prev })
	lib := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lib, "benchmarks"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &app{library: lib, logger: schemas.NoopLogger{}}
	warmLibrary(context.Background(), a, nil)
	if called {
		t.Error("warm ran with HEIMDALL_WARM_SUITES=off")
	}
}

// The hosted server answers /readyz and /healthz while the warm is still
// loading packs: the warm runs beside the listener, never before it.
func TestReadyzBeforeWarmEnds(t *testing.T) {
	f := newHostedFixture(t)
	lib := t.TempDir()
	if err := os.CopyFS(filepath.Join(lib, "benchmarks", "coding-core"), os.DirFS(filepath.Join("..", "..", "benchmarks", "coding-core"))); err != nil {
		t.Fatal(err)
	}
	a := f.env.base
	prevLib := a.library
	a.library = lib
	t.Cleanup(func() { a.library = prevLib })
	entered := make(chan []string, 1)
	release := make(chan struct{})
	prevRun, prevDelay := warmRun, warmLibraryDelay
	warmRun = func(ctx context.Context, h *handlers.SuitesHandler, ids []string, l schemas.Logger) <-chan struct{} {
		entered <- ids
		<-release // a pack load that takes a long time
		return prevRun(ctx, h, ids, l)
	}
	warmLibraryDelay = 0
	t.Cleanup(func() { warmRun, warmLibraryDelay = prevRun, prevDelay })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	warmDone := make(chan struct{})
	go func() {
		defer close(warmDone)
		warmLibrary(ctx, a, f.env)
	}()
	select {
	case ids := <-entered:
		if len(ids) != 1 || ids[0] != "coding-core" {
			t.Errorf("warm picked %v, want [coding-core]", ids)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("warm never started")
	}
	// /readyz answers at once and none of its checks waits on the warm (the
	// fixture's own migrations check may fail: its app config names another
	// database than the fixture's; that is not about the warm).
	start := time.Now()
	r := f.do("GET", "/readyz", "", nil, nil)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("/readyz took %s during the warm", took)
	}
	if r.status != 200 && r.status != 503 || strings.Contains(string(r.body), "suite") {
		t.Errorf("/readyz during the warm: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/healthz", "", nil, nil); r.status != 200 {
		t.Errorf("/healthz during the warm: %d %s", r.status, r.body)
	}
	select {
	case <-warmDone:
		t.Fatal("warm finished before its pack was released")
	default:
	}
	close(release)
	cancel()
	select {
	case <-warmDone:
	case <-time.After(30 * time.Second):
		t.Fatal("warm did not end after shutdown")
	}
}
