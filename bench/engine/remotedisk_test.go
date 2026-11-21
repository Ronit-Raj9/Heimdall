package engine

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestRemoteDiskLowTakesNoLease is E6: a runner below its free disk floor
// says so with its lease request; the coordinator records it and leases
// nothing, and leases again once the disk is back.
func TestRemoteDiskLowTakesNoLease(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	ask := func(low bool) *schemas.WorkerLease {
		var l schemas.WorkerLease
		req := schemas.WorkerLeaseRequest{WorkerID: w.ID(), Disk: &schemas.WorkerDisk{FreeBytes: 1 << 30, MinFreeBytes: 10 << 30, Low: low}}
		if err := w.call(context.Background(), http.MethodPost, workerAPI+"/lease", req, &l); err != nil {
			t.Fatal(err)
		}
		if l.LeaseID == "" {
			return nil
		}
		return &l
	}
	if l := ask(true); l != nil {
		t.Fatalf("leased %s to a runner whose disk is low", l.LeaseID)
	}
	ws := rw.c.Workers()
	if len(ws) != 1 || ws[0].Disk == nil || !ws[0].Disk.Low || ws[0].Disk.MinFreeBytes != 10<<30 {
		t.Fatalf("workers = %+v", ws)
	}
	if st := rw.c.Stats(); st.WorkersDiskLow != 1 || st.Leases != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if l := ask(false); l == nil {
		t.Fatal("no lease once the disk was back")
	}
	if st := rw.c.Stats(); st.WorkersDiskLow != 0 || st.Leases != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// TestRemoteAcceptedRunFolderRemoved is E6: once the coordinator accepted an
// upload, the runner deletes its local copy (sealed, read-only folders
// included); KeepRunFolders keeps it.
func TestRemoteAcceptedRunFolderRemoved(t *testing.T) {
	for _, keep := range []bool{false, true} {
		rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
		done := rw.attach(t)
		side := newRemoteSide(t)
		w := rw.worker(t, "w1", side)
		w.opts.KeepRunFolders = keep
		var used []string
		w.opts.ImageUse = func(ids ...string) { used = append(used, ids...) }
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease")
		}
		res := w.Work(context.Background(), l)
		if res == nil || !res.Accepted {
			t.Fatalf("not accepted: %+v", res)
		}
		dirs, err := filepath.Glob(filepath.Join(side.st.Root(), "*", "slots", "*", "runs", "*"))
		if err != nil {
			t.Fatal(err)
		}
		if keep != (len(dirs) == 1) || (!keep && len(dirs) != 0) {
			t.Fatalf("keep=%v: run folders left %v", keep, dirs)
		}
		accepted := 0
		for k, n := range rw.c.Stats().Runs {
			if strings.HasPrefix(k, "accepted/") {
				accepted += n
			}
		}
		if accepted != 1 {
			t.Fatalf("stats runs = %v", rw.c.Stats().Runs)
		}
		_ = used
		done()
	}
}
