package evidencestore

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/store"
)

func TestServiceNotify(t *testing.T) {
	ctx := context.Background()
	runs := t.TempDir()
	t.Cleanup(func() { makeWritable(runs) })
	st, _ := store.NewFSStore(runs, nil)
	f := newExperiment(t, Org{ID: "local", Store: st}, "e_svc")
	f.sealed("s_1", "r_1")
	mir, d := newDirMirror(t)
	svc := NewService(mir, &RunsRootOrgs{Root: runs}, 0)
	svc.Debounce, svc.FirstSweep = 10*time.Millisecond, time.Hour
	svc.Start(ctx)
	defer svc.Close()
	svc.Notify("local", "e_svc")
	svc.Notify("local", "e_svc")
	svc.Notify("local", "../bad")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := d.Head(ctx, listingKey("local", "e_svc")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not mirror after Notify")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var nilSvc *Service
	nilSvc.Notify("local", "e_svc") // a nil service is a no-op
}

func TestServiceWithoutIntervalNeverSweeps(t *testing.T) {
	ctx := context.Background()
	runs := t.TempDir()
	t.Cleanup(func() { makeWritable(runs) })
	st, _ := store.NewFSStore(runs, nil)
	newExperiment(t, Org{ID: "local", Store: st}, "e_quiet").sealed("s_1", "r_1")
	mir, d := newDirMirror(t)
	svc := NewService(mir, &RunsRootOrgs{Root: runs}, 0)
	svc.FirstSweep = time.Millisecond
	svc.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	svc.Close()
	if _, err := d.Head(ctx, listingKey("local", "e_quiet")); err == nil {
		t.Fatal("service with no interval swept")
	}
}
