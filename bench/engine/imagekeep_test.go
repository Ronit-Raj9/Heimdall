package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// libraryRoot lays out a library benchmarks folder: an imported suite with a
// locked prebuilt task image, a draft task (not in the lock) with its own
// prebuilt image, a built task image, a service and a fingerprinted base.
func libraryRoot(t *testing.T) string {
	root := filepath.Join(t.TempDir(), "benchmarks")
	s := filepath.Join(root, "swe-lite")
	writeFile(t, filepath.Join(s, "suite.yaml"), "id: swe-lite\nbase_image:\n  tag: bench/swe-lite-base:1\nservices:\n  - id: db\n    image: postgres:16\n")
	writeFile(t, filepath.Join(s, "suite.lock"), `{"suite":"swe-lite","base_image":{"tag":"bench/swe-lite-base:1"},"tasks":{"django-1":{"status":"admitted"}},`+
		`"fingerprints":{"version":1,"suite":{"settings":"","files":"","images":{"helper":"tag only: bench/coding-core-base:1"}},"tasks":{"django-1":{"settings":"","files":"","images":{"task":"bench/sweb.eval.django-1:latest=bench/sweb@sha256:abc"}}}}}`)
	writeFile(t, filepath.Join(s, "tasks", "django-1", "task.yaml"), "id: django-1\nenvironment:\n  image: bench/sweb.eval.django-1:latest\n")
	writeFile(t, filepath.Join(s, "tasks", "draft-2", "task.yaml"), "id: draft-2\nenvironment:\n  image: bench/sweb.eval.draft-2\n")
	writeFile(t, filepath.Join(s, "tasks", "built-3", "task.yaml"), "id: built-3\nenvironment:\n  dockerfile: Dockerfile\n")
	writeFile(t, filepath.Join(root, "README.md"), "not a suite")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSuiteImageKeeper(t *testing.T) {
	root := libraryRoot(t)
	k := &SuiteImageKeeper{Roots: func() ([]string, error) { return []string{root, filepath.Join(t.TempDir(), "missing")}, nil }, Extra: []string{"bench/pinned"}}
	keep, err := k.KeepSet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		"bench/sweb.eval.django-1:latest",          // locked task's prebuilt image
		"bench/sweb.eval.draft-2:latest",           // draft task's image (tag-less reference)
		"bench/swe-lite-base:1",                    // base image
		"postgres:16",                              // suite service
		"bench/coding-core-base:1",                 // fingerprinted helper
		"bench/task-swe-lite-built-3:0123456789ab", // built task image, any content hash
		"bench/task-swe-lite-other:1",              // any task of the suite
		"bench/pinned:latest",
	} {
		if !keep.Keeps(ref) {
			t.Errorf("keep set drops %s", ref)
		}
	}
	for _, ref := range []string{"bench/agent-x:1", "bench/task-other-suite-a:1", "bench/swe-lite-base:2"} {
		if keep.Keeps(ref) {
			t.Errorf("keep set keeps %s, which no suite needs", ref)
		}
	}
	// Cached by stamp: a new task.yaml is picked up.
	writeFile(t, filepath.Join(root, "swe-lite", "tasks", "new-4", "task.yaml"), "id: new-4\nenvironment:\n  image: bench/new:7\n")
	keep, err = k.KeepSet(context.Background())
	if err != nil || !keep.Keeps("bench/new:7") {
		t.Fatalf("new task image not kept: %v", err)
	}
}

func TestSuiteImageKeeperReadErrorFails(t *testing.T) {
	root := libraryRoot(t)
	writeFile(t, filepath.Join(root, "swe-lite", "suite.lock"), "{broken")
	k := &SuiteImageKeeper{Roots: func() ([]string, error) { return []string{root}, nil }}
	if _, err := k.KeepSet(context.Background()); err == nil {
		t.Fatal("a lock that does not parse built a keep set")
	}
}

// TestHygieneGCKeepsSuiteImages: image GC never removes an image a suite
// needs, however long unused, and removes nothing when the keep set fails.
func TestHygieneGCKeepsSuiteImages(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	old := listTime(now.Add(-30 * 24 * time.Hour))
	images := strings.Join([]string{
		"sha256:lib\tbench/sweb.eval.django-1:latest\t" + old,
		"sha256:built\tbench/task-swe-lite-built-3:0123456789ab\t" + old,
		"sha256:agent\tbench/agent-x:1\t" + old,
	}, "\n")
	root := libraryRoot(t)
	k := &SuiteImageKeeper{Roots: func() ([]string, error) { return []string{root}, nil }}
	docker, logPath := hygieneDocker(t, images, "", "", "")
	h := NewHygiene(nil, docker, HygieneOptions{ImageMaxUnused: 48 * time.Hour, Keep: k.KeepSet})
	h.now = func() time.Time { return now }
	n, err := h.GCImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	if n != 1 || !strings.Contains(string(raw), "image rm bench/agent-x:1") || strings.Contains(string(raw), "sweb") || strings.Contains(string(raw), "task-swe-lite") {
		t.Fatalf("removed %d:\n%s", n, raw)
	}
	writeFile(t, filepath.Join(root, "swe-lite", "suite.lock"), "{broken")
	docker, logPath = hygieneDocker(t, images, "", "", "")
	h = NewHygiene(nil, docker, HygieneOptions{ImageMaxUnused: 48 * time.Hour, Keep: k.KeepSet})
	h.now = func() time.Time { return now }
	if _, err := h.GCImages(context.Background()); err == nil {
		t.Fatal("GC ran without a keep set")
	}
	raw, _ = os.ReadFile(logPath)
	if strings.Contains(string(raw), "image rm") {
		t.Fatalf("removed images without a keep set:\n%s", raw)
	}
}
