package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// hygieneDocker is a docker stub for image GC: it logs every call and answers
// the image listing, the snapshot listing, ps and container inspect.
func hygieneDocker(t *testing.T, images, snapshots, containers, inspect string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$*" in
  "image ls -q"*) cat %q ;;
  "image ls"*) cat %q ;;
  "ps -a -q"*) cat %q ;;
  "container inspect"*) cat %q ;;
esac
exit 0
`, logPath, write("snap", snapshots), write("images", images), write("ps", containers), write("inspect", inspect))
	bin := write("docker", script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func TestHygieneGCImages(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	old := listTime(now.Add(-100 * time.Hour))
	young := listTime(now.Add(-time.Hour))
	images := strings.Join([]string{
		"sha256:old\tbench/task-a:111\t" + old,            // unused 100h: removed
		"sha256:old\tbench/task-a:latest\t" + old,         // same image, second tag: removed with it
		"sha256:young\tbench/task-b:222\t" + young,        // created 1h ago: kept
		"sha256:inuse\tbench/agent-x:1\t" + old,           // a stopped container uses it: kept
		"sha256:pinned\tbench/coding-core-base:1\t" + old, // pinned helper: kept
		"sha256:foreign\tacme/tool:1\t" + old,             // not a Heimdall namespace: kept
		"sha256:mixed\tbench/z:1\t" + old,                 // also tagged outside bench/: kept
		"sha256:mixed\tother/z:1\t" + old,                 //
		"sha256:org\torg-acme/agent:3\t" + old,            // organization namespace: removed
		"sha256:recent\tbench/task-c:333\t" + old,         // old, but a run used it 2h ago: kept
		"sha256:platform\theimdall/server:local\t" + old,  // the platform's own: kept
		"sha256:dangling\t<none>:<none>\t" + old,          // untagged: kept (no reference)
		"sha256:snap\tbench/snap-r1:1\t" + old,            // run snapshot: the janitor's
	}, "\n")
	docker, logPath := hygieneDocker(t, images, "sha256:snap\n", "c1\n", "sha256:inuse\tbench/agent-x:1\n")
	store := t.TempDir()
	h := NewHygiene(nil, docker, HygieneOptions{StoreRoot: store, ImageMaxUnused: 48 * time.Hour, Pinned: []string{"bench/coding-core-base:1"}})
	h.now = func() time.Time { return now.Add(-2 * time.Hour) }
	h.NoteImages("sha256:recent")
	h.now = func() time.Time { return now }
	n, err := h.GCImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	var rms []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "image rm") {
			rms = append(rms, l)
		}
	}
	want := []string{"image rm bench/task-a:111 bench/task-a:latest", "image rm org-acme/agent:3"}
	if n != 2 || strings.Join(rms, "|") != strings.Join(want, "|") {
		t.Fatalf("removed %d: %q, want %q", n, rms, want)
	}
	if strings.Contains(string(raw), "image rm -f") {
		t.Fatal("image GC must not force-remove")
	}
	if _, err := os.Stat(filepath.Join(store, ".runner", imageUsageFile)); err != nil {
		t.Fatalf("usage file: %v", err)
	}
}

func TestHygieneGCImagesListingFailsRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	log := filepath.Join(dir, "log")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\ncase \"$*\" in \"ps\"*) exit 1 ;; \"image ls -q\"*) ;; \"image ls\"*) printf 'sha256:a\\tbench/a:1\\t2020-01-01 00:00:00 +0000 UTC\\n' ;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := NewHygiene(nil, utils.Docker{Binary: bin}, HygieneOptions{ImageMaxUnused: time.Hour})
	if _, err := h.GCImages(context.Background()); err == nil {
		t.Fatal("a failed container listing must fail the GC")
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), "image rm") {
		t.Fatalf("removed an image without knowing what is in use:\n%s", raw)
	}
}

func TestHygieneBuildCachePruneOnlyOwnBuilder(t *testing.T) {
	docker, logPath := hygieneDocker(t, "", "", "", "")
	h := NewHygiene(nil, docker, HygieneOptions{BuildCacheKeep: 10 << 30})
	if ok, err := h.PruneBuildCache(context.Background()); ok || err != nil {
		t.Fatalf("pruned without a builder: %v %v", ok, err)
	}
	h = NewHygiene(nil, docker, HygieneOptions{Builder: "heimdall", BuildCacheKeep: 10 << 30})
	if ok, err := h.PruneBuildCache(context.Background()); !ok || err != nil {
		t.Fatalf("prune: %v %v", ok, err)
	}
	raw, _ := os.ReadFile(logPath)
	if got := strings.TrimSpace(string(raw)); got != "buildx prune --builder heimdall --force --keep-storage 10240mb" {
		t.Fatalf("calls = %q", got)
	}
}

func TestHygieneDiskState(t *testing.T) {
	h := NewHygiene(nil, utils.Docker{}, HygieneOptions{StoreRoot: filepath.Join(t.TempDir(), "not", "yet"), MinFreeBytes: 10 << 30})
	h.free = func(string) (int64, error) { return 5 << 30, nil }
	if d := h.DiskState(); !d.Low || d.FreeBytes != 5<<30 || d.MinFreeBytes != 10<<30 {
		t.Fatalf("disk = %+v", d)
	}
	h.free = func(string) (int64, error) { return 20 << 30, nil }
	if d := h.DiskState(); d.Low {
		t.Fatalf("disk = %+v", d)
	}
	h.free = func(string) (int64, error) { return 0, fmt.Errorf("no statfs") }
	if d := h.DiskState(); d.Low || d.FreeBytes != -1 {
		t.Fatalf("unmeasurable disk must not block: %+v", d)
	}
	if real := NewHygiene(nil, utils.Docker{}, HygieneOptions{StoreRoot: t.TempDir()}).DiskState(); real.FreeBytes <= 0 {
		t.Fatalf("statfs of a temp dir: %+v", real)
	}
}

func TestHygieneSweepRunsJanitorOnStore(t *testing.T) {
	now := time.Now()
	runs, _ := leaseWorld(t, now)
	docker, logPath := fakeDocker(t, "", map[string]string{"ps -a": "c_stale\tr_stale\t" + listTime(now) + "\n"})
	h := NewHygiene(NewJanitor(nil, docker, schemas.NoopLogger{}), docker, HygieneOptions{StoreRoot: runs})
	rep := h.Sweep(context.Background())
	if rep.Leftovers == nil || rep.Leftovers.Containers != 1 {
		t.Fatalf("report = %+v", rep.Leftovers)
	}
	if rm := removals(t, logPath); len(rm) != 1 || !strings.Contains(rm[0], "c_stale") {
		t.Fatalf("removals = %v", rm)
	}
}
