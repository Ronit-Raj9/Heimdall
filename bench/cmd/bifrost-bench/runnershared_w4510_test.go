package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestReconcileRestartsOnPlatformListChange (N208 fact 3): an org's loop
// restarts when a per-evaluation platform is added, rotated or removed,
// not only when the default platform changes; the key holds no token and
// does not depend on the order of the list.
func TestReconcileRestartsOnPlatformListChange(t *testing.T) {
	loops := map[string]*sharedLoop{}
	started := 0
	start := func(a sharedpool.Assignment, slots int) *sharedLoop {
		started++
		d := make(chan struct{})
		l := &sharedLoop{slots: slots, platform: platformsKey(a), done: d, policy: &utils.DockerPolicy{}}
		l.cancel = func() { close(d) }
		return l
	}
	as := &sharedpool.Assignments{Assignments: []sharedpool.Assignment{{OrgID: "a"}}}
	reconcileShared(loops, as, 2, start)
	if started != 1 {
		t.Fatalf("start: %d", started)
	}
	modal := &sharedpool.PlatformAssignment{Name: "modal", Stamp: "s1"}
	e2b := &sharedpool.PlatformAssignment{Name: "e2b", Stamp: "s2"}
	as.Assignments[0].Platforms = []*sharedpool.PlatformAssignment{modal}
	reconcileShared(loops, as, 2, start)
	if started != 2 {
		t.Fatalf("platform added: started %d", started)
	}
	reconcileShared(loops, as, 2, start)
	if started != 2 {
		t.Fatal("restarted an unchanged platform list")
	}
	as.Assignments[0].Platforms = []*sharedpool.PlatformAssignment{{Name: "modal", Stamp: "s9"}}
	reconcileShared(loops, as, 2, start)
	if started != 3 {
		t.Fatalf("platform token rotated: started %d", started)
	}
	as.Assignments[0].Platforms = []*sharedpool.PlatformAssignment{modal, e2b}
	reconcileShared(loops, as, 2, start)
	as.Assignments[0].Platforms = []*sharedpool.PlatformAssignment{e2b, modal}
	reconcileShared(loops, as, 2, start)
	if started != 4 {
		t.Fatalf("order of the list restarted the loop: started %d", started)
	}
	as.Assignments[0].Platforms = nil
	reconcileShared(loops, as, 2, start)
	if started != 5 {
		t.Fatalf("platform removed: started %d", started)
	}
	if k := platformsKey(sharedpool.Assignment{Platform: &sharedpool.PlatformAssignment{Name: "modal", Token: "ak:as"}}); k == "" || strings.Contains(k, "ak:as") {
		t.Fatalf("key %q", k)
	}
}
