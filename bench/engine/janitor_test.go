package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDocker writes a shell script that logs its arguments and prints canned
// listings, so the janitor runs with no docker daemon. listings maps the
// first two arguments ("ps -a", "volume ls", "network ls", "volume inspect")
// to what the call prints; failList names the call that exits 1.
func fakeDocker(t *testing.T, failList string, listings map[string]string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	cases := ""
	for call, out := range listings {
		f := filepath.Join(dir, strings.ReplaceAll(call, " ", "_")+".out")
		if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		cases += fmt.Sprintf("  %q) cat %q ;;\n", call, f)
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1 $2" in
  %q) echo "daemon down" >&2; exit 1 ;;
%sesac
exit 0
`, logPath, failList, cases)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

// legacyListings are listings without creation times, as an old fake or an
// unreadable format would give: no run without a lease is ever old enough.
var legacyListings = map[string]string{
	"ps -a":      "c1\tr_dead\nc2\tr_live\nc3\t\n",
	"volume ls":  "v1\tr_dead\nv2\t<no value>\n",
	"network ls": "n1\tr_live\n",
}

// writeLease writes a slot's lease.json as the store does, heartbeat at hb.
func writeLease(t *testing.T, slotDir, owner string, hb time.Time) {
	t.Helper()
	b, err := json.Marshal(schemas.Lease{SlotID: filepath.Base(slotDir), Owner: owner, HeartbeatAt: hb})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(slotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotDir, leaseName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRun makes a run folder under a slot, sealed or not.
func writeRun(t *testing.T, slotDir, runID string, sealed bool) {
	t.Helper()
	dir := filepath.Join(slotDir, runsFolder, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if sealed {
		if err := os.WriteFile(filepath.Join(dir, sealName), []byte("{}"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
}

// leaseWorld lays out a runs root and a checks root: r_live in a slot with a
// fresh lease held by another owner, r_stale in a slot whose lease is an
// hour old, r_done sealed in a slot with no lease, and r_check, an agent
// check run (no lease, not sealed).
func leaseWorld(t *testing.T, now time.Time) (runs, checks string) {
	t.Helper()
	root := t.TempDir()
	runs, checks = filepath.Join(root, "runs"), filepath.Join(root, "checks")
	live := filepath.Join(runs, "e_other", slotsFolder, "s_live")
	writeLease(t, live, "w_other", now.Add(-10*time.Second))
	writeRun(t, live, "r_live", false)
	stale := filepath.Join(runs, "e_other", slotsFolder, "s_stale")
	writeLease(t, stale, "w_crashed", now.Add(-time.Hour))
	writeRun(t, stale, "r_stale", false)
	done := filepath.Join(runs, "e_mine", slotsFolder, "s_done")
	writeRun(t, done, "r_done", true)
	writeRun(t, filepath.Join(checks, "check_x", slotsFolder, "s_c"), "r_check", false)
	return runs, checks
}

func listTime(t time.Time) string    { return t.Format("2006-01-02 15:04:05.999999999 -0700 MST") }
func inspectTime(t time.Time) string { return t.Format(time.RFC3339Nano) }

// removals returns the rm calls the fake docker logged, and fails on a
// listing without the bench label filter.
func removals(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.HasPrefix(line, "rm ") || strings.HasPrefix(line, "volume rm ") || strings.HasPrefix(line, "network rm ") || strings.HasPrefix(line, "image rm ") {
			removed = append(removed, line)
		}
		if strings.Contains(line, " ls") || strings.HasPrefix(line, "ps") {
			if !strings.Contains(line, "--filter label=bifrost-bench=1") {
				t.Errorf("listing without the bench label filter: %s", line)
			}
		}
	}
	return removed
}

func TestJanitorSweep(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))
	old, young := now.Add(-48*time.Hour), now.Add(-time.Minute)
	listings := map[string]string{
		"ps -a": strings.Join([]string{
			"c_live\tr_live\t" + listTime(old),     // fresh foreign lease: kept however old
			"c_stale\tr_stale\t" + listTime(young), // stale lease: removed however young
			"c_done\tr_done\t" + listTime(young),   // sealed, no lease: removed
			"c_check\tr_check\t" + listTime(young), // agent check, no lease: young, kept
			"c_old\tr_gone\t" + listTime(old),      // unknown run, old: removed
			"c_young\tr_new\t" + listTime(young),   // unknown run, young: kept
			"c_odd\tr_odd\tyesterday",              // unreadable age: kept
			"c_infra\t\t" + listTime(old),          // no run label: shared, never touched
		}, "\n") + "\n",
		"volume ls": "v_live\tr_live\nv_old\tr_gone\nv_young\tr_new\nv_check\tr_check\nv_infra\t<no value>\n",
		"volume inspect": "v_live\t" + inspectTime(old) + "\nv_old\t" + inspectTime(old) + "\nv_young\t" + inspectTime(young) +
			"\nv_check\t" + inspectTime(young) + "\n",
		"network ls": "n_stale\tr_stale\t" + listTime(young) + "\nn_old\tr_gone\t" + listTime(old) + "\n",
	}
	tests := []struct {
		name     string
		failList string
		wantRm   []string
		wantKeep []string // the active set handed to RevokeStale; nil: not called
		wantErr  []string
		want     SweepReport
	}{
		{
			name:     "leases decide, age decides only without a lease",
			failList: "none",
			wantRm:   []string{"rm -f -v c_stale c_done c_old", "volume rm -f v_old", "network rm n_stale n_old"},
			wantKeep: []string{"r_check", "r_live", "r_new", "r_odd"},
			want:     SweepReport{Containers: 3, Volumes: 1, Networks: 2, Keys: 2, KeptYoung: 2},
		},
		{
			name:     "a failed listing removes only runs whose lease says dead and revokes no key",
			failList: "ps -a",
			wantRm:   []string{"network rm n_stale"},
			wantErr:  []string{"list containers", "stale keys not revoked"},
			want:     SweepReport{Networks: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			runs, checks := leaseWorld(t, now)
			lv, err := ScanRunLiveness([]string{runs, checks, filepath.Join(t.TempDir(), "missing")}, DefaultStaleAfter, now)
			if err != nil {
				t.Fatal(err)
			}
			docker, logPath := fakeDocker(t, tt.failList, listings)
			j := NewJanitor(b.gw, docker, nil, WithSweepGrace(time.Hour))
			j.now = func() time.Time { return now }
			rep, err := j.Sweep(context.Background(), lv)
			for _, w := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Fatalf("err = %v, want %q", err, w)
				}
			}
			if tt.wantErr == nil && err != nil {
				t.Fatal(err)
			}
			if *rep != tt.want {
				t.Fatalf("report %+v, want %+v", *rep, tt.want)
			}
			if got := removals(t, logPath); strings.Join(got, "|") != strings.Join(tt.wantRm, "|") {
				t.Fatalf("removed %v, want %v", got, tt.wantRm)
			}
			if tt.wantKeep == nil {
				if b.w.count("revoke_stale") != 0 {
					t.Fatal("keys revoked although a listing failed")
				}
				return
			}
			var keep []string
			for run := range b.gw.active {
				keep = append(keep, run)
			}
			sort.Strings(keep)
			if b.w.count("revoke_stale") != 1 || strings.Join(keep, ",") != strings.Join(tt.wantKeep, ",") {
				t.Fatalf("keys kept for %v, want %v", keep, tt.wantKeep)
			}
		})
	}
}

func TestScanRunLiveness(t *testing.T) {
	now := time.Now()
	runs, checks := leaseWorld(t, now)
	creating := filepath.Join(runs, "e_new", slotsFolder, "s_new")
	writeRun(t, creating, "r_creating", false)
	if err := os.WriteFile(filepath.Join(creating, leaseName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lv, err := ScanRunLiveness([]string{runs, checks}, DefaultStaleAfter, now)
	if err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]bool) string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	// A lease.json being created (empty) is fresh from its mtime; the check
	// run has no lease information at all.
	if got := keys(lv.Live); got != "r_creating,r_live" {
		t.Fatalf("live = %s", got)
	}
	if got := keys(lv.Dead); got != "r_done,r_stale" {
		t.Fatalf("dead = %s", got)
	}
}

func TestParseObjects(t *testing.T) {
	out := []byte("a\tr_1\t2026-11-15 00:27:01 +0530 IST\nb\tr_2\nc\t\nd\t<no value>\n\ne\tr_3\t2026-11-14 04:57:53.823533264 +0530 IST\n")
	got := parseObjects(out)
	if len(got) != 3 || got[0].id != "a" || got[1].run != "r_2" || !got[1].created.IsZero() || got[2].id != "e" {
		t.Fatalf("got %+v", got)
	}
	if want := time.Date(2026, 11, 13, 23, 27, 53, 823533264, time.UTC); !got[2].created.Equal(want) {
		t.Fatalf("network time %v, want %v", got[2].created, want)
	}
	if want := time.Date(2026, 11, 14, 18, 57, 1, 0, time.UTC); !got[0].created.Equal(want) {
		t.Fatalf("container time %v, want %v", got[0].created, want)
	}
	if at, ok := parseDockerTime("2026-11-14T23:41:24+05:30"); !ok || !at.Equal(time.Date(2026, 11, 14, 18, 11, 24, 0, time.UTC)) {
		t.Fatalf("inspect time %v %t", at, ok)
	}
}

func TestSchedulerRunsJanitor(t *testing.T) {
	b := newBench(t)
	docker, _ := fakeDocker(t, "none", legacyListings)
	m := b.plan(t, testExperiment())
	if _, err := NewScheduler(b.reg, b.root, nil, NewJanitor(b.gw, docker, nil)).Run(context.Background(), m, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if b.w.count("revoke_stale") != 1 {
		t.Fatal("janitor did not sweep at scheduler start")
	}
}

// TestSchedulerSweepKeepsForeignLiveRun starts a scheduler while another
// process holds a fresh lease on a slot of a different experiment under the
// same runs root, and while an agent check is running: neither run's
// containers or keys may be touched, however old they are, while a crashed
// run's leftovers go.
func TestSchedulerSweepKeepsForeignLiveRun(t *testing.T) {
	b := newBench(t)
	now := time.Now()
	foreign := filepath.Join(b.store.root, "e_foreign", slotsFolder, "s_foreign")
	writeLease(t, foreign, "w_other_process", now.Add(-5*time.Second))
	writeRun(t, foreign, "r_foreign", false)
	crashed := filepath.Join(b.store.root, "e_foreign", slotsFolder, "s_crashed")
	writeLease(t, crashed, "w_dead_process", now.Add(-time.Hour))
	writeRun(t, crashed, "r_crashed", false)
	writeRun(t, filepath.Join(b.root, ChecksRoot, "check_a", slotsFolder, "s_c"), "r_check", false)
	old := now.Add(-48 * time.Hour)
	docker, logPath := fakeDocker(t, "none", map[string]string{
		"ps -a":      "c_foreign\tr_foreign\t" + listTime(old) + "\nc_crashed\tr_crashed\t" + listTime(now) + "\nc_check\tr_check\t" + listTime(now) + "\n",
		"volume ls":  "",
		"network ls": "",
	})
	m := b.plan(t, testExperiment())
	if _, err := NewScheduler(b.reg, b.root, nil, NewJanitor(b.gw, docker, nil)).Run(context.Background(), m, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := removals(t, logPath); strings.Join(got, "|") != "rm -f -v c_crashed" {
		t.Fatalf("removed %v, want only the crashed run's container", got)
	}
	if !b.gw.active["r_foreign"] || !b.gw.active["r_check"] || b.gw.active["r_crashed"] {
		t.Fatalf("keys kept for %v", b.gw.active)
	}
}

// TestJanitorSweepsSnapshotImages checks the image kind: an image listing
// cannot print labels, so labelled images are read with inspect, and only a
// dead run's snapshot image goes; a live run's stays.
func TestJanitorSweepsSnapshotImages(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	runs, checks := leaseWorld(t, now)
	lv, err := ScanRunLiveness([]string{runs, checks}, DefaultStaleAfter, now)
	if err != nil {
		t.Fatal(err)
	}
	docker, logPath := fakeDocker(t, "none", map[string]string{
		"ps -a":         "",
		"volume ls":     "",
		"network ls":    "",
		"image ls":      "sha256:aaa\nsha256:bbb\nsha256:aaa\n",
		"image inspect": "sha256:aaa\tr_stale\t" + inspectTime(now) + "\nsha256:bbb\tr_live\t" + inspectTime(now.Add(-48*time.Hour)) + "\n",
	})
	j := NewJanitor(nil, docker, nil)
	j.now = func() time.Time { return now }
	rep, err := j.Sweep(context.Background(), lv)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Images != 1 {
		t.Fatalf("report %+v, want one image", *rep)
	}
	if got := removals(t, logPath); strings.Join(got, "|") != "image rm -f sha256:aaa" {
		t.Fatalf("removed %v", got)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "image inspect --format") || strings.Count(string(raw), "sha256:aaa") != 2 {
		t.Fatalf("calls:\n%s", raw)
	}
}

// TestScanRunLivenessUnreadableSlots is the 2-B-12 regression: a slots
// folder that cannot be read fails the scan instead of hiding its leases,
// so the janitor sweeps nothing. A missing root is still nothing to scan.
func TestScanRunLivenessUnreadableSlots(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any folder")
	}
	now := time.Now()
	runs, _ := leaseWorld(t, now)
	if _, err := ScanRunLiveness([]string{filepath.Join(t.TempDir(), "absent")}, DefaultStaleAfter, now); err != nil {
		t.Fatalf("missing root: %v", err)
	}
	hidden := filepath.Join(runs, "e_hidden", slotsFolder)
	writeRun(t, filepath.Join(hidden, "s_h"), "r_hidden", false)
	if err := os.Chmod(hidden, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hidden, 0o755) })
	if _, err := ScanRunLiveness([]string{runs}, DefaultStaleAfter, now); err == nil {
		t.Fatal("unreadable slots folder scanned as no lease information")
	}
}
