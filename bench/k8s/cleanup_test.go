package k8s

import (
	"testing"
	"time"
)

// 2-D-14: cleanup selects only this install's namespaces, and never a live
// or young run.
func TestClassifyCleanup(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	old, young := now.Add(-48*time.Hour).Format(time.RFC3339), now.Add(-time.Minute).Format(time.RFC3339)
	ns := func(name, run, role, created string) Namespace {
		return Namespace{Metadata: ObjectMeta{Name: name, CreationTimestamp: created, Labels: map[string]string{LabelBench: "1", LabelRun: run, LabelRole: role}}}
	}
	items := []Namespace{
		ns("heimdall-run-r-live", "r_live", RoleRun, old),
		ns("heimdall-run-r-dead", "r_dead", RoleRun, young),
		ns("heimdall-run-r-young", "r_young", RoleRun, young),
		ns("heimdall-run-r-old", "r_old", RoleRun, old),
		ns("heimdall-run-r-noage", "r_noage", RoleRun, ""),
		ns("other-install-r-x", "r_x", RoleRun, old),
		ns("heimdall-run-gw", "", RoleGW, old),
	}
	got := map[string]bool{}
	for _, v := range ClassifyCleanup(items, CleanupInput{Prefix: "heimdall-run", Live: map[string]bool{"r_live": true},
		Dead: map[string]bool{"r_dead": true}, Grace: 6 * time.Hour, Now: now}) {
		got[v.Namespace.Metadata.Name] = v.Remove
	}
	want := map[string]bool{"heimdall-run-r-live": false, "heimdall-run-r-dead": true, "heimdall-run-r-young": false,
		"heimdall-run-r-old": true, "heimdall-run-r-noage": false}
	if len(got) != len(want) {
		t.Fatalf("selected %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s remove=%v, want %v", k, got[k], v)
		}
	}
}

// QC2-10-02: with an install id, another install's namespaces under the same
// default prefix are never selected, an old one of this install is, and an
// unlabelled (legacy) one only when this install's store shows it ended.
func TestClassifyCleanupInstallOwner(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour).Format(time.RFC3339)
	ns := func(name, run, install string) Namespace {
		l := map[string]string{LabelBench: "1", LabelRun: run, LabelRole: RoleRun}
		if install != "" {
			l[LabelInstall] = install
		}
		return Namespace{Metadata: ObjectMeta{Name: name, CreationTimestamp: old, Labels: l}}
	}
	items := []Namespace{
		ns("heimdall-run-r-mine", "r_mine", "me"),
		ns("heimdall-run-r-theirs", "r_theirs", "them"),
		ns("heimdall-run-r-legacy", "r_legacy", ""),
		ns("heimdall-run-r-legacy-dead", "r_legacy_dead", ""),
	}
	got := map[string]bool{}
	for _, v := range ClassifyCleanup(items, CleanupInput{Prefix: "heimdall-run", Install: "me", Dead: map[string]bool{"r_legacy_dead": true}, Grace: 6 * time.Hour, Now: now}) {
		got[v.Namespace.Metadata.Name] = v.Remove
	}
	want := map[string]bool{"heimdall-run-r-mine": true, "heimdall-run-r-legacy": false, "heimdall-run-r-legacy-dead": true}
	if len(got) != len(want) {
		t.Fatalf("selected %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s remove=%v, want %v", k, got[k], v)
		}
	}
	if id := InstallID("/srv/a"); len(id) != 16 || id == InstallID("/srv/b") {
		t.Fatalf("install ids %q", id)
	}
}
