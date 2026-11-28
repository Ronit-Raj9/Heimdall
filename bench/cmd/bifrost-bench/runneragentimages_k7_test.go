package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// k7Builder is a fake agent image builder: it records each build, fails the
// tags in fail, and can hold every build until release is closed.
type k7Builder struct {
	mu      sync.Mutex
	built   []string
	fail    map[string]bool
	release chan struct{}
	running atomic.Int32
	maxRun  atomic.Int32
}

func (b *k7Builder) BuildAgentImage(ctx context.Context, img engine.AgentImage) (string, error) {
	n := b.running.Add(1)
	defer b.running.Add(-1)
	for {
		m := b.maxRun.Load()
		if n <= m || b.maxRun.CompareAndSwap(m, n) {
			break
		}
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.built = append(b.built, img.Tag)
	if b.fail[img.Tag] {
		return "", errors.New("pinned package not found")
	}
	return "sha256:" + img.Tag, nil
}

// k7Fixture is an org project with a base_arg agent and a suite of n tasks
// that each bring their own image, and a manifest whose arm records each
// task's per-task tag, as the hosted plan does.
func k7Fixture(t *testing.T, n int) (string, *schemas.Manifest, *schemas.Suite) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "Dockerfile"), []byte("ARG BASE\nFROM ${BASE}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &schemas.Suite{ID: "hd-tb2"}
	s.BaseImage.Tag = "bench/base:1"
	cfg := schemas.AgentConfig{ID: "codex", Adapter: "cli", Image: "bench/agent:1-hd-tb2",
		Build: &schemas.ImageSpec{Dockerfile: "agents/Dockerfile", Tag: "bench/agent:1", BaseArg: "BASE"}}
	am := schemas.ArmManifest{Config: cfg, ConfigHash: "cfg1", TaskImages: map[string]string{}}
	m := &schemas.Manifest{ExpID: "e1", Arms: map[string]schemas.ArmManifest{}}
	for i := range n {
		id := "t" + string(rune('a'+i))
		s.Tasks = append(s.Tasks, &schemas.Task{ID: id, Dir: filepath.Join(root, "benchmarks", "hd-tb2", "tasks", id),
			Environment: &schemas.TaskEnvironment{Image: "ghcr.io/x/" + id + "@sha256:" + strings.Repeat("a", 64)}})
	}
	for i := range s.Tasks {
		task := s.Tasks[i]
		ti, ok, err := utils.TaskImage(s.ID, task)
		if err != nil || !ok {
			t.Fatalf("task image %v %v", ok, err)
		}
		c := cfg
		c.Image = c.Build.Tag
		img, err := engine.ResolveTaskAgentImage(&c, s, task, ti.Tag, root, "")
		if err != nil || !img.PerTask {
			t.Fatalf("resolve %+v %v", img, err)
		}
		am.TaskImages[task.ID] = img.Tag
		m.Slots = append(m.Slots, schemas.Slot{SlotID: "s" + task.ID, Arm: "A", Task: task.ID})
	}
	m.Arms["A"] = am
	return root, m, s
}

func k7Slot(m *schemas.Manifest, task string) *schemas.Slot {
	for i := range m.Slots {
		if m.Slots[i].Task == task {
			return &m.Slots[i]
		}
	}
	return nil
}

// A slot builds only its own task's image, once; later slots of the task
// reuse it; progress names the image and its place.
func TestSlotAgentImagesBuildsOnlyTheSlotsTaskOnce(t *testing.T) {
	root, m, s := k7Fixture(t, 3)
	fb := &k7Builder{}
	x := newSlotAgentImages(fb, root, "")
	var lines []string
	ctx := utils.WithImageProgress(context.Background(), func(msg string) { lines = append(lines, msg) })
	if err := x.ensure(ctx, m, s, k7Slot(m, "tb")); err != nil {
		t.Fatal(err)
	}
	if len(fb.built) != 1 || fb.built[0] != m.Arms["A"].TaskImages["tb"] {
		t.Fatalf("built %v, want only task tb's image", fb.built)
	}
	if err := x.ensure(ctx, m, s, k7Slot(m, "tb")); err != nil {
		t.Fatal(err)
	}
	if err := x.ensure(ctx, m, s, k7Slot(m, "ta")); err != nil {
		t.Fatal(err)
	}
	if len(fb.built) != 2 {
		t.Fatalf("built %v, want tb once then ta", fb.built)
	}
	want := []string{"Building agent image 1 of 3: task tb", "Building agent image 2 of 3: task ta"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("progress %q, want %q", lines, want)
	}
}

// One task's failed build fails that task's slots only, and fails them at
// once on a retry instead of building again.
func TestSlotAgentImagesFailedBuildStopsOnlyItsTask(t *testing.T) {
	root, m, s := k7Fixture(t, 2)
	fb := &k7Builder{fail: map[string]bool{m.Arms["A"].TaskImages["ta"]: true}}
	x := newSlotAgentImages(fb, root, "")
	err := x.ensure(context.Background(), m, s, k7Slot(m, "ta"))
	if err == nil || !strings.Contains(err.Error(), "for task ta") || !strings.Contains(err.Error(), "pinned package not found") {
		t.Fatalf("error %v", err)
	}
	if err := x.ensure(context.Background(), m, s, k7Slot(m, "tb")); err != nil {
		t.Fatalf("task tb stopped by task ta's build: %v", err)
	}
	if err := x.ensure(context.Background(), m, s, k7Slot(m, "ta")); err == nil {
		t.Fatal("retry of the failed task succeeded")
	}
	if len(fb.built) != 2 {
		t.Fatalf("built %v: the failed build ran again", fb.built)
	}
	now := time.Now().Add(failedBuildTTL + time.Minute)
	x.now = func() time.Time { return now }
	_ = x.ensure(context.Background(), m, s, k7Slot(m, "ta"))
	if len(fb.built) != 3 {
		t.Fatalf("built %v: a failed build is never tried again", fb.built)
	}
}

// Two slots of the same task at once start one build; the second waits.
func TestSlotAgentImagesConcurrentSlotsShareOneBuild(t *testing.T) {
	root, m, s := k7Fixture(t, 1)
	fb := &k7Builder{release: make(chan struct{})}
	x := newSlotAgentImages(fb, root, "")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = x.ensure(context.Background(), m, s, k7Slot(m, "ta")) }()
	}
	time.Sleep(50 * time.Millisecond)
	close(fb.release)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || len(fb.built) != 1 || fb.maxRun.Load() != 1 {
		t.Fatalf("errs %v built %v max running %d", errs, fb.built, fb.maxRun.Load())
	}
}

// A slot whose arm records no per-task image (an own-image agent, a task
// without its own image) builds nothing.
func TestSlotAgentImagesSkipsSlotsWithoutPerTaskImage(t *testing.T) {
	root, m, s := k7Fixture(t, 1)
	am := m.Arms["A"]
	am.TaskImages = nil
	m.Arms["A"] = am
	fb := &k7Builder{}
	if err := newSlotAgentImages(fb, root, "").ensure(context.Background(), m, s, k7Slot(m, "ta")); err != nil || len(fb.built) != 0 {
		t.Fatalf("err %v built %v", err, fb.built)
	}
}

// Isolation (K7): each org's builds go through that org's own policy
// docker, so its build sessions take that org's lock and cache volume; two
// orgs never share either.
func TestBuildSessionIsolationPerOrg(t *testing.T) {
	a, b := newSessionNames("org_a", "1"), newSessionNames("org_b", "1")
	if a.volume == b.volume || a.builder == b.builder || a.buildkit == b.buildkit {
		t.Fatalf("orgs share a session object: %+v %+v", a, b)
	}
	if again := newSessionNames("org_a", "2"); again.volume != a.volume || again.buildkit == a.buildkit {
		t.Fatalf("an org's sessions: cache %s/%s, engine %s/%s", a.volume, again.volume, a.buildkit, again.buildkit)
	}
	la, lb := buildSessionArgv("x", "org_a", "r", "/l"), buildSessionArgv("x", "org_b", "r", "/l")
	if strings.Join(la, " ") == strings.Join(lb, " ") {
		t.Fatal("two orgs get the same build session command")
	}
}
