package main

// Image prewarm for evaluations (G4-04, handlers.Prewarmer). When an
// evaluation creates an experiment on a launcher that runs slots on this
// host, the suite's images (base and services) and every arm's agent image
// are pulled or built in the background right away, one experiment at a
// time, so the first runs do not each wait on a multi-minute pull. Task
// images and the agents built on them are already made present by the plan
// (engine.Planner.taskImages). A hosted launcher runs no slot here: its
// runner makes every task image of an experiment present when it first
// plans it (sharedEnsureImages), so this does nothing there. A failure only
// logs: the run pulls or builds the image itself, as before.

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// prewarmTimeout bounds one experiment's prewarm.
const prewarmTimeout = 30 * time.Minute

// prewarmMu runs one prewarm at a time on this host, so prewarms never
// compete with each other for the network.
var prewarmMu sync.Mutex

var _ handlers.Prewarmer = (*launcher)(nil)

// Prewarm implements handlers.Prewarmer.
func (l *launcher) Prewarm(m *schemas.Manifest) {
	if l == nil || l.remoteOnly || l.a == nil || m == nil {
		return
	}
	go func() {
		prewarmMu.Lock()
		defer prewarmMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), prewarmTimeout)
		defer cancel()
		l.a.prewarm(ctx, m)
	}()
}

// prewarm makes m's suite images and arm agent images present.
func (a *app) prewarm(ctx context.Context, m *schemas.Manifest) {
	ref := m.SuiteID
	if m.SuiteVersion != "" {
		ref += "@" + m.SuiteVersion
	}
	s, err := engine.LoadSuite(ctx, a.reg, ref)
	if err != nil {
		a.logger.Warn("prewarm: load suite", "experiment", m.ExpID, "error", err.Error())
		return
	}
	if err := ops.EnsureImages(ctx, a.cfg, a.docker, s, io.Discard); err != nil {
		a.logger.Warn("prewarm: suite images", "experiment", m.ExpID, "error", err.Error())
	}
	builder := engine.DockerAgentImages{Docker: a.docker}
	for _, img := range prewarmAgentImages(m, s, a.cfg.Root) {
		if _, ok, err := a.docker.ImageID(ctx, img.Tag); err == nil && ok {
			continue
		}
		var err error
		if img.Dockerfile != "" {
			_, err = builder.BuildAgentImage(ctx, img)
		} else {
			_, err = a.docker.EnsureImage(ctx, utils.LocalImage{Tag: img.Tag}, io.Discard)
		}
		if err != nil {
			a.logger.Warn("prewarm: agent image", "experiment", m.ExpID, "image", img.Tag, "error", err.Error())
		}
	}
}

// prewarmAgentImages lists the distinct agent images m's arms start on
// suite s, as the run resolves them (engine.ResolveAgentImage), per-suite
// builds included. An arm whose image cannot be resolved is left to its run.
func prewarmAgentImages(m *schemas.Manifest, s *schemas.Suite, root string) []engine.AgentImage {
	var out []engine.AgentImage
	seen := map[string]bool{}
	for _, arm := range m.Arms {
		cfg := arm.Config
		img, err := engine.ResolveAgentImage(&cfg, s, root)
		if err != nil || img.Tag == "" || seen[img.Tag] {
			continue
		}
		seen[img.Tag] = true
		out = append(out, img)
	}
	return out
}
