package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLeaseLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	mustCreateExp(t, s, "e_test")
	const slot = "s_a1"
	steps := []struct {
		name    string
		op      func() (bool, error)
		want    bool
		wantErr error
	}{
		{name: "acquire", op: func() (bool, error) { return s.Lease(ctx, slot, "w1", time.Minute) }, want: true},
		{name: "other owner refused", op: func() (bool, error) { return s.Lease(ctx, slot, "w2", time.Minute) }},
		{name: "same owner again", op: func() (bool, error) { return s.Lease(ctx, slot, "w1", time.Minute) }, want: true},
		{name: "heartbeat owner", op: func() (bool, error) { return true, s.Heartbeat(ctx, slot, "w1") }, want: true},
		{name: "heartbeat other", op: func() (bool, error) { return false, s.Heartbeat(ctx, slot, "w2") }, wantErr: schemas.ErrLeaseHeld},
		{name: "release other", op: func() (bool, error) { return false, s.Release(ctx, slot, "w2") }, wantErr: schemas.ErrLeaseHeld},
		{name: "release owner", op: func() (bool, error) { return true, s.Release(ctx, slot, "w1") }, want: true},
		{name: "release again is no-op", op: func() (bool, error) { return true, s.Release(ctx, slot, "w1") }, want: true},
		{name: "heartbeat after release", op: func() (bool, error) { return false, s.Heartbeat(ctx, slot, "w1") }, wantErr: schemas.ErrNotFound},
		{name: "other owner now wins", op: func() (bool, error) { return s.Lease(ctx, slot, "w2", time.Minute) }, want: true},
		{name: "unknown slot", op: func() (bool, error) { return s.Lease(ctx, "s_nope", "w1", time.Minute) }, wantErr: schemas.ErrNotFound},
		{name: "bad slot id", op: func() (bool, error) { return s.Lease(ctx, "../s", "w1", time.Minute) }, wantErr: errAny},
		{name: "empty owner", op: func() (bool, error) { return s.Lease(ctx, slot, "", time.Minute) }, wantErr: errAny},
		{name: "zero stale_after", op: func() (bool, error) { return s.Lease(ctx, slot, "w1", 0) }, wantErr: errAny},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			got, err := st.op()
			switch {
			case st.wantErr == errAny:
				if err == nil {
					t.Fatal("want error")
				}
			case st.wantErr != nil:
				if !errors.Is(err, st.wantErr) {
					t.Fatalf("err = %v, want %v", err, st.wantErr)
				}
			case err != nil:
				t.Fatalf("err = %v", err)
			case got != st.want:
				t.Fatalf("got %v, want %v", got, st.want)
			}
		})
	}
}

// errAny marks a step that must fail with any error.
var errAny = errors.New("any error")

func TestHeartbeatAdvances(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	mustCreateExp(t, s, "e_test")
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return t0 }
	if ok, err := s.Lease(ctx, "s_a1", "w1", time.Minute); !ok || err != nil {
		t.Fatalf("lease = %v, %v", ok, err)
	}
	s.now = func() time.Time { return t0.Add(30 * time.Second) }
	if err := s.Heartbeat(ctx, "s_a1", "w1"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	l, _, err := s.readLease(filepath.Join(s.Root(), "e_test", "slots", "s_a1", "lease.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !l.HeartbeatAt.Equal(t0.Add(30*time.Second)) || l.Owner != "w1" || l.SlotID != "s_a1" {
		t.Fatalf("lease = %+v", l)
	}
}

// TestLeaseConcurrent races 10 goroutines for one free slot, through one
// shared store (mutex path) and through 10 stores on the same root (O_EXCL and
// flock path, as separate processes would).
func TestLeaseConcurrent(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared_store=%v", shared), func(t *testing.T) {
			s := newTestStore(t, nil)
			mustCreateExp(t, s, "e_test")
			stores := make([]*FSStore, 10)
			for i := range stores {
				stores[i] = s
				if !shared {
					var err error
					if stores[i], err = NewFSStore(s.Root(), nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			winners := raceLease(t, stores, time.Minute)
			if winners != 1 {
				t.Fatalf("winners = %d, want exactly 1", winners)
			}
		})
	}
}

func TestLeaseStaleReclaim(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		age        time.Duration
		staleAfter time.Duration
		want       bool
	}{
		{name: "live lease kept", age: 10 * time.Second, staleAfter: time.Minute, want: false},
		{name: "stale lease reclaimed", age: 2 * time.Minute, staleAfter: time.Minute, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t, nil)
			mustCreateExp(t, s, "e_test")
			now := time.Now()
			s.now = func() time.Time { return now.Add(-tc.age) }
			if ok, err := s.Lease(ctx, "s_a1", "crashed", tc.staleAfter); !ok || err != nil {
				t.Fatalf("first lease = %v, %v", ok, err)
			}
			other, err := NewFSStore(s.Root(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := other.Lease(ctx, "s_a1", "resumer", tc.staleAfter)
			if err != nil || got != tc.want {
				t.Fatalf("reclaim = %v, %v, want %v", got, err, tc.want)
			}
			s.now = time.Now
			hbErr := s.Heartbeat(ctx, "s_a1", "crashed")
			if tc.want && !errors.Is(hbErr, schemas.ErrLeaseHeld) {
				t.Fatalf("old owner heartbeat err = %v, want ErrLeaseHeld", hbErr)
			}
			if !tc.want && hbErr != nil {
				t.Fatalf("live owner heartbeat err = %v", hbErr)
			}
			assertNoTombs(t, filepath.Join(s.Root(), "e_test", "slots", "s_a1"))
		})
	}
}

// TestLeaseStaleConcurrent races 10 reclaimers, each its own store, for one
// stale lease: exactly one may win.
func TestLeaseStaleConcurrent(t *testing.T) {
	for round := range 5 {
		t.Run(fmt.Sprintf("round_%d", round), func(t *testing.T) {
			s := newTestStore(t, nil)
			mustCreateExp(t, s, "e_test")
			s.now = func() time.Time { return time.Now().Add(-time.Hour) }
			if ok, err := s.Lease(context.Background(), "s_a1", "crashed", time.Minute); !ok || err != nil {
				t.Fatalf("stale lease = %v, %v", ok, err)
			}
			stores := make([]*FSStore, 10)
			for i := range stores {
				var err error
				if stores[i], err = NewFSStore(s.Root(), nil); err != nil {
					t.Fatal(err)
				}
			}
			if winners := raceLease(t, stores, time.Minute); winners != 1 {
				t.Fatalf("winners = %d, want exactly 1", winners)
			}
			assertNoTombs(t, filepath.Join(s.Root(), "e_test", "slots", "s_a1"))
		})
	}
}

func TestLeaseUnreadableFileIsNotReclaimedEarly(t *testing.T) {
	ctx := context.Background()
	log := &recLogger{}
	s := newTestStore(t, log)
	mustCreateExp(t, s, "e_test")
	// An empty lease.json is a create in progress; its fresh mtime keeps it.
	p := filepath.Join(s.Root(), "e_test", "slots", "s_a1", "lease.json")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Lease(ctx, "s_a1", "w1", time.Minute); ok || err != nil {
		t.Fatalf("lease = %v, %v, want false", ok, err)
	}
	if log.count("lease.json unreadable") == 0 {
		t.Fatal("unreadable lease not reported")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Lease(ctx, "s_a1", "w1", time.Minute); !ok || err != nil {
		t.Fatalf("stale unreadable lease: %v, %v, want reclaimed", ok, err)
	}
}

func raceLease(t *testing.T, stores []*FSStore, staleAfter time.Duration) int {
	t.Helper()
	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	for i, st := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := st.Lease(context.Background(), "s_a1", fmt.Sprintf("w%d", i), staleAfter)
			if err != nil {
				errs <- err
				return
			}
			if ok {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Lease: %v", err)
	}
	return int(winners.Load())
}

func assertNoTombs(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "lease.json.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 0 {
		t.Fatalf("leftover lease files: %v", matches)
	}
}
