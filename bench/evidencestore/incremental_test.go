package evidencestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var incOpts = MirrorOptions{Incremental: true}

func listingOf(t *testing.T, m *Mirror, orgID, expID string) *Listing {
	t.Helper()
	l, _, err := readListing(context.Background(), m.Blob, orgID, expID)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// An incremental pass after new seals leaves the same listing a full pass
// would.
func TestIncrementalPassMatchesFull(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_incr")
	full, _ := newDirMirror(t)
	inc, _ := newDirMirror(t)
	for _, m := range []*Mirror{full, inc} {
		if _, err := m.Experiment(ctx, org, "e_incr"); err != nil {
			t.Fatal(err)
		}
	}
	f.seal("r_4")
	f.analysis("a_2")
	if _, err := full.Experiment(ctx, org, "e_incr"); err != nil {
		t.Fatal(err)
	}
	res, err := inc.ExperimentWith(ctx, org, "e_incr", incOpts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Seals != 4 || len(res.Pending) != 0 {
		t.Fatalf("incremental pass: %+v", res)
	}
	a, b := listingOf(t, full, "local", "e_incr"), listingOf(t, inc, "local", "e_incr")
	if a.Seals != b.Seals || a.LastSeal != b.LastSeal || !reflect.DeepEqual(a.Files, b.Files) {
		t.Fatalf("listings differ:\nfull %+v\n inc %+v", a, b)
	}
	again, err := inc.ExperimentWith(ctx, org, "e_incr", incOpts)
	if err != nil || !again.Unchanged {
		t.Fatalf("second incremental pass: %+v, %v", again, err)
	}
}

// An incremental pass verifies the new runs only: a tampered new run is
// refused, a tampered old run is left to the full pass, which refuses it.
func TestIncrementalPassVerifiesNewRunsOnly(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_split")
	m, _ := newDirMirror(t)
	if _, err := m.Experiment(ctx, org, "e_split"); err != nil {
		t.Fatal(err)
	}
	f.seal("r_4")
	tamper(t, filepath.Join(f.runDir("r_4"), "workspace.diff"), []byte("evil\n"))
	if _, err := m.ExperimentWith(ctx, org, "e_split", incOpts); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "workspace.diff") {
		t.Fatalf("tampered new run: %v", err)
	}

	org2 := newOrg(t, "local")
	g := standard(t, org2, "e_old")
	m2, _ := newDirMirror(t)
	if _, err := m2.Experiment(ctx, org2, "e_old"); err != nil {
		t.Fatal(err)
	}
	tamper(t, filepath.Join(g.runDir("r_1"), "workspace.diff"), []byte("evil\n"))
	g.seal("r_4")
	if _, err := m2.ExperimentWith(ctx, org2, "e_old", incOpts); err != nil {
		t.Fatalf("incremental pass read an old run: %v", err)
	}
	if _, err := m2.Experiment(ctx, org2, "e_old"); !errors.Is(err, ErrRefused) {
		t.Fatalf("full pass missed the tampered old run: %v", err)
	}
}

// A seals.log that no longer starts with the mirrored seals is not
// incremental: the full pass runs and refuses it.
func TestIncrementalPassFallsBackOnRewrittenLog(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_rw")
	m, _ := newDirMirror(t)
	if _, err := m.Experiment(ctx, org, "e_rw"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.dir(), sealsLogFile)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(b), "\n")
	if err := os.WriteFile(p, []byte(lines[1]+lines[0]+strings.Join(lines[2:], "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ExperimentWith(ctx, org, "e_rw", incOpts); !errors.Is(err, ErrRefused) {
		t.Fatalf("rewritten log: %v", err)
	}
}

func TestIncrementalFromNeedsPrefix(t *testing.T) {
	h := func(c string) string { return strings.Repeat(c, 64) }
	entries := []sealEntry{{RunID: "r_1", Hash: h("a")}, {RunID: "r_2", Hash: h("b")}}
	cases := []struct {
		name string
		base *Listing
		want bool
	}{
		{"none", nil, false},
		{"empty", &Listing{ManifestHash: "m"}, false},
		{"prefix", &Listing{ManifestHash: "m", Seals: 1, LastSeal: h("a")}, true},
		{"whole", &Listing{ManifestHash: "m", Seals: 2, LastSeal: h("b")}, true},
		{"other hash", &Listing{ManifestHash: "m", Seals: 1, LastSeal: h("b")}, false},
		{"longer", &Listing{ManifestHash: "m", Seals: 3, LastSeal: h("c")}, false},
		{"other manifest", &Listing{ManifestHash: "x", Seals: 1, LastSeal: h("a")}, false},
	}
	for _, c := range cases {
		if got := incrementalFrom(c.base, entries, "m") != nil; got != c.want {
			t.Errorf("%s: incremental %v, want %v", c.name, got, c.want)
		}
	}
	inc := incrementalFrom(&Listing{ManifestHash: "m", Seals: 1, LastSeal: h("a")}, append(entries, sealEntry{RunID: "r_1", Hash: h("c")}), "m")
	if err := inc.checkLog(append(entries, sealEntry{RunID: "r_1", Hash: h("c")})); err == nil {
		t.Fatal("a run named twice: want error")
	}
}

func TestServiceNotifyFullIsNotDowngraded(t *testing.T) {
	s := NewService(nil, nil, 0)
	s.NotifyFull("local", "e_1")
	s.Notify("local", "e_1")
	s.Notify("local", "e_2")
	if !s.pending[[2]string{"local", "e_1"}] || s.pending[[2]string{"local", "e_2"}] {
		t.Fatalf("pending %v", s.pending)
	}
}
