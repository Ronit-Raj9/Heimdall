//go:build unix

package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrgBuildSlotsSetting(t *testing.T) {
	for v, want := range map[string]int{"": defaultOrgBuilds, "x": defaultOrgBuilds, "2": 2, "1": 1, "0": 1, "-3": 1, " 3 ": 3, "99": maxOrgBuilds} {
		if got := orgBuildSlots(v); got != want {
			t.Errorf("orgBuildSlots(%q) = %d, want %d", v, got, want)
		}
	}
	// One slot is exactly today's single lock and single cache volume.
	if got := orgBuildLocks("/l", "org_a", 1); len(got) != 1 || got[0] != "/l/org_a.lock" {
		t.Errorf("one slot locks %v", got)
	}
	if got := orgBuildLocks("/l", "org_a", 3); strings.Join(got, " ") != "/l/org_a.lock /l/org_a.1.lock /l/org_a.2.lock" {
		t.Errorf("three slot locks %v", got)
	}
	if n := newSlotSessionNames("org_a", "s1", 0); n.volume != "heimdall-buildcache-org_a" {
		t.Errorf("slot 0 volume %q", n.volume)
	}
	if n := newSlotSessionNames("org_a", "s1", 2); n.volume != "heimdall-buildcache-org_a-2" {
		t.Errorf("slot 2 volume %q", n.volume)
	}
}

// At most N holders per org at once; other orgs are not limited by it;
// a released slot is taken by a waiter.
func TestLockOrgBuildSlotLimit(t *testing.T) {
	dir := t.TempDir()
	locks := orgBuildLocks(dir, "org_a", 2)
	var running, peak int32
	var wg sync.WaitGroup
	slotsSeen := sync.Map{}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, slot, err := lockOrgBuildSlot(context.Background(), locks)
			if err != nil {
				t.Error(err)
				return
			}
			slotsSeen.Store(slot, true)
			n := atomic.AddInt32(&running, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(300 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			unlock()
		}()
	}
	wg.Wait()
	if peak != 2 {
		t.Fatalf("peak %d builds at once, want 2", peak)
	}
	for _, k := range []int{0, 1} {
		if _, ok := slotsSeen.Load(k); !ok {
			t.Errorf("slot %d never used", k)
		}
	}
	// Both slots of org_a held: org_b still gets one at once, org_a waits.
	u0, _, _ := lockOrgBuildSlot(context.Background(), locks)
	u1, _, _ := lockOrgBuildSlot(context.Background(), locks)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if _, _, err := lockOrgBuildSlot(ctx, locks); err == nil {
		t.Fatal("third org_a build got a slot while two were held")
	}
	ub, _, err := lockOrgBuildSlot(context.Background(), orgBuildLocks(dir, "org_b", 2))
	if err != nil {
		t.Fatal(err)
	}
	ub()
	u0()
	u1()
}

// A session whose org's slot 0 is busy runs on slot 1 with that slot's own
// cache volume; a failed build releases its slot for the next one.
func TestBuildSessionSecondSlot(t *testing.T) {
	bin, dir := fakeDocker(t)
	lockDir := filepath.Join(dir, "locks")
	hold, slot, err := lockOrgBuildSlot(context.Background(), orgBuildLocks(lockDir, "org_a", 1))
	if err != nil || slot != 0 {
		t.Fatal(err)
	}
	defer hold()
	c := &buildSessionConfig{Org: "org_a", Runner: "shared-x", LockDir: lockDir, Slots: 2, Docker: bin,
		Network: "heimdall-buildnet", BuildkitImage: "moby/buildkit@sha256:1", GuardImage: "heimdall/runner:local",
		newID: func() string { return "s2" }}
	code, err := c.run(context.Background(), []string{"buildx", "build", "--load", "-t", "org-org-a/x:1", "/ctx"})
	if err != nil || code == 0 {
		t.Fatalf("fake build exit code not passed through: %d %v", code, err)
	}
	bk := callWith(calls(t, dir), "run -d --rm --name heimdall-build-org_a-s2-buildkit")
	if !strings.Contains(bk, "-v heimdall-buildcache-org_a-1:/home/user/.local/share/buildkit") {
		t.Fatalf("second slot did not use its own cache volume: %q", bk)
	}
	if strings.Contains(bk, "heimdall-buildcache-org_a:") {
		t.Fatalf("second slot mounted slot 0's cache: %q", bk)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	u, s, err := lockOrgBuildSlot(ctx, orgBuildLocks(lockDir, "org_a", 2))
	if err != nil || s != 1 {
		t.Fatalf("slot 1 not released after the failed build: %d %v", s, err)
	}
	u()
}
