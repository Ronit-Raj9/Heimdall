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
	"sort"
	"sync"

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
		if am == nil || am.Config.Build == nil || am.Config.Build.BaseArg == "" || !armRunsSuiteImage(m, slotArm, am) {
			return
		}
		cfg := am.Config
		cfg.Image = cfg.Build.Tag                                                 // the manifest records the derived per-suite tag; resolve from the build tag, as the planner does
		img, err := engine.ResolveAgentImageIn(&cfg, s, root, suite.LibraryDir()) // library and built-in build files, as the planner resolves them
		if err != nil || !img.PerSuite || img.Dockerfile == "" || seen[img.Tag] {
			return // an image that cannot be resolved is left to its run, as before
		}
		seen[img.Tag] = true
		needs = append(needs, need{key: am.ConfigHash + "|" + s.ID + "|" + img.Tag + "|" + fileDigest(img.Dockerfile), img: img})
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
