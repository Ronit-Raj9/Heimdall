package tenancy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

type countingBuilder struct {
	builds atomic.Int32
	closes atomic.Int32
	fail   atomic.Bool
	delay  time.Duration
	busy   atomic.Bool
}

func (b *countingBuilder) build(_ context.Context, org Org, p OrgPaths) (*OrgRuntime, error) {
	b.builds.Add(1)
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	if b.fail.Load() {
		return nil, errors.New("boom")
	}
	return &OrgRuntime{
		Handler: func(ctx *fasthttp.RequestCtx) { ctx.SetBodyString(org.ID + "|" + p.StoreRoot) },
		Busy:    b.busy.Load,
		Close:   func() error { b.closes.Add(1); return nil },
	}, nil
}

func newTestRegistry(t *testing.T, b *countingBuilder, opts Options) *Registry {
	t.Helper()
	return NewRegistry(Layout{Hosted: true, StorageRoot: t.TempDir()}, b.build, opts)
}

func TestRegistryBuildsOncePerOrgConcurrently(t *testing.T) {
	b := &countingBuilder{delay: 20 * time.Millisecond}
	r := newTestRegistry(t, b, Options{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt, release, err := r.Acquire(context.Background(), Org{ID: "org_a"})
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			if rt.Org.ID != "org_a" {
				t.Errorf("runtime org = %q", rt.Org.ID)
			}
		}()
	}
	wg.Wait()
	if n := b.builds.Load(); n != 1 {
		t.Fatalf("builds = %d, want 1", n)
	}
	rtB, rel, err := r.Acquire(context.Background(), Org{ID: "org_b"})
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if rtB.Paths.StoreRoot == "" || rtB.Paths.StoreRoot == r.layout.StorageRoot {
		t.Fatalf("org b store root %q", rtB.Paths.StoreRoot)
	}
	if b.builds.Load() != 2 {
		t.Fatalf("builds = %d, want 2", b.builds.Load())
	}
}

func TestRegistryEvictClosesAfterLastRelease(t *testing.T) {
	b := &countingBuilder{}
	r := newTestRegistry(t, b, Options{})
	_, release, err := r.Acquire(context.Background(), Org{ID: "org_a"})
	if err != nil {
		t.Fatal(err)
	}
	r.Evict("org_a")
	if b.closes.Load() != 0 {
		t.Fatal("closed while held")
	}
	release()
	release() // idempotent
	if b.closes.Load() != 1 {
		t.Fatalf("closes = %d, want 1", b.closes.Load())
	}
	// The next request rebuilds.
	_, release, err = r.Acquire(context.Background(), Org{ID: "org_a"})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if b.builds.Load() != 2 {
		t.Fatalf("builds = %d, want 2", b.builds.Load())
	}
}

func TestRegistryFailedBuildIsRetried(t *testing.T) {
	b := &countingBuilder{}
	b.fail.Store(true)
	r := newTestRegistry(t, b, Options{})
	if _, _, err := r.Acquire(context.Background(), Org{ID: "org_a"}); err == nil {
		t.Fatal("want build error")
	}
	b.fail.Store(false)
	_, release, err := r.Acquire(context.Background(), Org{ID: "org_a"})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if b.builds.Load() != 2 {
		t.Fatalf("builds = %d", b.builds.Load())
	}
}

func TestRegistryRefusesBadOrgIDs(t *testing.T) {
	r := newTestRegistry(t, &countingBuilder{}, Options{})
	for _, id := range []string{"", "../x", "a/b", "ORG", "org.a", ".hidden", "org a"} {
		if _, _, err := r.Acquire(context.Background(), Org{ID: id}); err == nil {
			t.Errorf("org id %q accepted", id)
		}
	}
}

func TestRegistryEvictsOverflowAndIdle(t *testing.T) {
	now := time.Unix(1000, 0)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	b := &countingBuilder{}
	r := newTestRegistry(t, b, Options{MaxOrgs: 2, IdleTTL: time.Minute, Now: clock})
	for _, id := range []string{"org_a", "org_b", "org_c"} {
		_, release, err := r.Acquire(context.Background(), Org{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		release()
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
	}
	if got := r.Cached(); len(got) != 2 || got[0] != "org_b" {
		t.Fatalf("cached = %v, want org_b and org_c", got)
	}
	b.busy.Store(true)
	mu.Lock()
	now = now.Add(time.Hour)
	mu.Unlock()
	r.Sweep()
	if len(r.Cached()) != 2 {
		t.Fatal("busy runtimes were evicted")
	}
	b.busy.Store(false)
	r.Sweep()
	if len(r.Cached()) != 0 {
		t.Fatalf("idle runtimes kept: %v", r.Cached())
	}
	if b.closes.Load() != 3 {
		t.Fatalf("closes = %d, want 3", b.closes.Load())
	}
}

func TestRegistryClose(t *testing.T) {
	b := &countingBuilder{}
	r := newTestRegistry(t, b, Options{})
	_, release, _ := r.Acquire(context.Background(), Org{ID: "org_a"})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Acquire(context.Background(), Org{ID: "org_b"}); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("acquire after close: %v", err)
	}
	release()
	if b.closes.Load() != 1 {
		t.Fatalf("closes = %d", b.closes.Load())
	}
}

func TestLocalLayoutServesOnlyLocal(t *testing.T) {
	l := Layout{LocalRunsRoot: "/r", LocalBenchRoot: "/b", LocalReleasesRoot: "/rel"}
	p, err := l.Paths(LocalOrgID)
	if err != nil || p.StoreRoot != "/r" || p.ProjectRoot != "/b" || p.ReleasesRoot != "/rel" {
		t.Fatalf("local paths = %+v, %v", p, err)
	}
	if _, err := l.Paths("org_a"); !errors.Is(err, ErrLocalOnly) {
		t.Fatalf("local mode served org_a: %v", err)
	}
}
