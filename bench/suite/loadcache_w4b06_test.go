package suite

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakePackLoad returns a load function that answers a suite with n tasks,
// so the cache can be filled without writing packs to disk.
func fakePackLoad(n int) func(context.Context) (*schemas.Suite, error) {
	return func(context.Context) (*schemas.Suite, error) {
		s := &schemas.Suite{ID: "fake"}
		for range n {
			s.Tasks = append(s.Tasks, &schemas.Task{})
		}
		return s, nil
	}
}

// TestLoadCacheKeepsBigSuiteAcrossManySmallLoads checks a big pack stays
// cached while many small packs are loaded after it: small packs are evicted
// first, so loading the big one again reads nothing from disk.
func TestLoadCacheKeepsBigSuiteAcrossManySmallLoads(t *testing.T) {
	freshLoadCache(t)
	ctx := context.Background()
	before := FullLoads()
	big := t.TempDir()
	if _, err := cachedLoad(ctx, big, false, fakePackLoad(1500)); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		if _, err := cachedLoad(ctx, t.TempDir(), false, fakePackLoad(3)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cachedLoad(ctx, big, false, fakePackLoad(1500)); err != nil {
		t.Fatal(err)
	}
	if got := FullLoads() - before; got != 41 {
		t.Fatalf("full loads grew by %d, want 41 (the big pack was evicted)", got)
	}
}
