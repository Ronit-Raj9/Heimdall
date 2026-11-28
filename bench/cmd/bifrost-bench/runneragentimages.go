package main

// Agent images on the shared worker (fix wave 3, SEC-A, N180, N181). Every
// agent image the shared worker builds for an org, per task or per
// benchmark, is built with the org's policy docker (utils/dockerpolicy.go):
// an org-written Dockerfile runs only on the isolated builder, under the
// org's image namespace, never on the host daemon.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// sharedPlannerOptions are the options of an org's planner on the shared
// worker. Per-task agent images build with docker (the org's policy docker)
// and a planner that would otherwise fall back to a docker CLI without a
// policy refuses the build instead. Build output goes to out (nil discards
// it); a failed build's error carries its last lines either way.
func sharedPlannerOptions(docker utils.Docker, out io.Writer) []engine.PlannerOption {
	return []engine.PlannerOption{
		engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck(), engine.WithAgentLibrary(suite.LibraryDir()),
		engine.WithAgentImageBuilder(engine.DockerAgentImages{Docker: docker, Out: out}), engine.WithoutHostAgentBuilds(),
	}
}

// sharedAgentImages builds, on the shared worker, the per-benchmark agent
// images (build.base_arg) a leased experiment's arms start, the first time
// they are needed (N180). Nothing else builds them on a hosted setup: the
// planner only resolves their tag, and prewarm does nothing there. Builds
// use docker, the org's policy docker, so an org-written Dockerfile runs
// only on the isolated builder.
type sharedAgentImages struct {
	docker utils.Docker
	out    io.Writer // build output; nil discards it (a failure still names its last lines)

	mu    sync.Mutex
	built map[string]bool // config hash | suite | tag | Dockerfile digest of every image built here
}

func newSharedAgentImages(docker utils.Docker) *sharedAgentImages {
	return &sharedAgentImages{docker: docker, built: map[string]bool{}}
}

// ensure builds every per-benchmark agent image of m's arms (and its
// compatibility agent) on suite s that is not built yet. A failed build
// returns "prepare the agent container <tag> for <suite>: <reason and last
// build lines>", which refuses the lease as permanent, so the slots end
// with that reason instead of waiting.
func (x *sharedAgentImages) ensure(ctx context.Context, m *schemas.Manifest, s *schemas.Suite, root string) error {
	if m == nil || s == nil {
		return nil
	}
	type need struct {
		key string
		img engine.AgentImage
	}
	var needs []need
	seen := map[string]bool{}
	add := func(slotArm string, am *schemas.ArmManifest) {
		if am == nil || am.Config.Adapter == "scripted" || am.Config.Build == nil || am.Config.Build.Dockerfile == "" || !armRunsSuiteImage(m, slotArm, am) {
			return
		}
		cfg := am.Config
		perSuite := cfg.Build.BaseArg != ""
		if perSuite {
			cfg.Image = cfg.Build.Tag // the manifest records the derived per-suite tag; resolve from the build tag, as the planner does
		}
		img, err := engine.ResolveAgentImageIn(&cfg, s, root, suite.LibraryDir()) // library and built-in build files, as the planner resolves them
		if err != nil || img.PerSuite != perSuite || img.Dockerfile == "" || img.Tag == "" || seen[img.Tag] {
			return // an image that cannot be resolved is left to its run, as before
		}
		if !perSuite && !orgOwnedFile(root, img.Dockerfile) {
			// A built-in agent keeps its Dockerfile outside the project and its
			// plain image is built when the worker is installed: only an agent
			// made in the web agent builder (its Dockerfile is in the org's
			// project) is built here.
			return
		}
		seen[img.Tag] = true
		key := am.ConfigHash + "|" + s.ID + "|" + img.Tag + "|" + fileDigest(img.Dockerfile)
		if !perSuite {
			// M1: a plain build (a template agent's bench/agent-<id>:1) was
			// built by nobody on the shared worker unless the owner pressed
			// Prepare container. It depends only on its build inputs, so two
			// versions that differ in settings share one build.
			key = "plain|" + img.Tag + "|" + img.Context + "|" + fileDigest(img.Dockerfile)
		}
		needs = append(needs, need{key: key, img: img})
	}
	arms := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		arms = append(arms, id)
	}
	sort.Strings(arms)
	for _, id := range arms {
		am := m.Arms[id]
		add(id, &am)
	}
	add("compat", m.Compatibility)
	for _, n := range needs {
		if err := x.build(ctx, n.key, n.img); err != nil {
			return fmt.Errorf("prepare the agent container %s for %s: %w", n.img.Tag, s.ID, err)
		}
	}
	return nil
}

// build builds one image unless it was built here already. Without a
// policy (an org's own runner) an image already present is kept. With one,
// only a build made here counts: a global tag on the host daemon was not
// built under this org's policy.
func (x *sharedAgentImages) build(ctx context.Context, key string, img engine.AgentImage) error {
	x.mu.Lock()
	done := x.built[key]
	x.mu.Unlock()
	if done {
		return nil
	}
	if x.docker.Policy == nil {
		if _, ok, err := x.docker.ImageID(ctx, img.Tag); err == nil && ok {
			x.remember(key)
			return nil
		}
	}
	if _, err := (engine.DockerAgentImages{Docker: x.docker, Out: x.out}).BuildAgentImage(ctx, img); err != nil {
		return err
	}
	x.remember(key)
	return nil
}

func (x *sharedAgentImages) remember(key string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.built[key] = true
}

// armRunsSuiteImage reports whether a slot of the arm (slotArm: the arm id,
// or "compat") runs the per-benchmark image: a task without a per-task
// agent image of its own.
func armRunsSuiteImage(m *schemas.Manifest, slotArm string, am *schemas.ArmManifest) bool {
	for _, sl := range m.Slots {
		if sl.Arm == slotArm && am.TaskImages[sl.Task] == "" {
			return true
		}
	}
	return false
}

// orgOwnedFile reports whether path is a regular file inside the org's
// project folder root.
func orgOwnedFile(root, path string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

// fileDigest is the sha256 of a file's bytes, or "" when it cannot be read:
// an edited Dockerfile builds again.
func fileDigest(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// slotAgentImages builds, on the shared worker, the per-task agent image
// (build.base_arg on a task's own image) of one leased slot right before
// that slot runs (K7, engine.WorkerOptions.SlotAgentImages). Before, the
// replan built every task's image of the experiment, one after another,
// before the first slot ran (24 images, 29 minutes on the live worker).
//
// Isolation is unchanged: every build goes through builder, the org's own
// policy docker (one slotAgentImages per org worker), so each build still
// runs in a build session of that org (org lock, egress guard, the org's
// own cache volume). Builds of one key run once; a concurrent slot of the
// same task waits for that build. A failed build is remembered for
// failedBuildTTL, so its retries of the same slot fail at once.
type slotAgentImages struct {
	builder engine.AgentImageBuilder
	root    string
	library string
	now     func() time.Time

	mu    sync.Mutex
	keys  map[string]*slotBuild
	built map[string]int // experiment id -> its builds started here, for the progress line
}

type slotBuild struct {
	done   chan struct{}
	err    error
	failed time.Time
}

// failedBuildTTL is how long a failed per-task build is not tried again.
const failedBuildTTL = 10 * time.Minute

func newSlotAgentImages(b engine.AgentImageBuilder, root, library string) *slotAgentImages {
	return &slotAgentImages{builder: b, root: root, library: library, now: time.Now, keys: map[string]*slotBuild{}, built: map[string]int{}}
}

// slotArm is the arm manifest a slot runs: the arm, or the compatibility
// agent for "compat".
func slotArm(m *schemas.Manifest, slot *schemas.Slot) *schemas.ArmManifest {
	if am, ok := m.Arms[slot.Arm]; ok {
		return &am
	}
	if slot.Arm == "compat" {
		return m.Compatibility
	}
	return nil
}

// perTaskImageCount is the number of distinct per-task agent images m's
// arms record, for "image n of N".
func perTaskImageCount(m *schemas.Manifest) int {
	tags := map[string]bool{}
	add := func(am *schemas.ArmManifest) {
		if am == nil || am.Config.Adapter == "scripted" {
			return
		}
		for _, t := range am.TaskImages {
			tags[t] = true
		}
	}
	for _, am := range m.Arms {
		add(&am)
	}
	add(m.Compatibility)
	return len(tags)
}

// ensure implements engine.WorkerOptions.SlotAgentImages.
func (x *slotAgentImages) ensure(ctx context.Context, m *schemas.Manifest, s *schemas.Suite, slot *schemas.Slot) error {
	if m == nil || s == nil || slot == nil {
		return nil
	}
	am := slotArm(m, slot)
	if am == nil || am.Config.Adapter == "scripted" || am.Config.Build == nil || am.Config.Build.BaseArg == "" || am.TaskImages[slot.Task] == "" {
		return nil
	}
	task := s.TaskByID(slot.Task)
	if task == nil {
		return nil
	}
	taskImg, ok, err := utils.TaskImage(s.ID, task)
	if err != nil || !ok {
		return err
	}
	cfg := am.Config
	cfg.Image = cfg.Build.Tag // as the planner resolves it (engine.buildTaskAgents)
	img, err := engine.ResolveTaskAgentImage(&cfg, s, task, taskImg.Tag, x.root, x.library)
	if err != nil {
		return fmt.Errorf("prepare the agent container for task %s: %w", slot.Task, err)
	}
	if !img.PerTask {
		return nil
	}
	if img.Tag != am.TaskImages[slot.Task] {
		return fmt.Errorf("prepare the agent container for task %s: it resolves to %s here but the plan recorded %s", slot.Task, img.Tag, am.TaskImages[slot.Task])
	}
	key := am.ConfigHash + "|" + s.ID + "|" + img.Tag + "|" + fileDigest(img.Dockerfile)
	if err := x.build(ctx, key, img, m.ExpID, slot.Task, perTaskImageCount(m)); err != nil {
		return fmt.Errorf("prepare the agent container %s for task %s: %w", img.Tag, slot.Task, err)
	}
	return nil
}

func (x *slotAgentImages) build(ctx context.Context, key string, img engine.AgentImage, exp, task string, total int) error {
	x.mu.Lock()
	b := x.keys[key]
	if b != nil && b.err != nil && x.now().Sub(b.failed) >= failedBuildTTL {
		b = nil // try a failed build again after a while
	}
	if b != nil {
		x.mu.Unlock()
		select {
		case <-b.done:
			return b.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b = &slotBuild{done: make(chan struct{})}
	x.keys[key] = b
	x.built[exp]++
	n := x.built[exp]
	x.mu.Unlock()
	msg := fmt.Sprintf("Building the agent image for task %s", task)
	if total > 1 {
		msg = fmt.Sprintf("Building agent image %d of %d: task %s", min(n, total), total, task)
	}
	utils.ImageProgress(ctx)(msg)
	_, err := x.builder.BuildAgentImage(ctx, img)
	x.mu.Lock()
	b.err = err
	if err != nil {
		b.failed = x.now()
		if ctx.Err() != nil {
			delete(x.keys, key) // a cancelled lease did not fail the build: the next slot tries again
		}
	}
	x.mu.Unlock()
	close(b.done)
	return err
}
