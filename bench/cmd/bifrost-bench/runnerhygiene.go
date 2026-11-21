package main

import (
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/engine"
)

// newRunnerHygiene is the disk upkeep of `runner start` and the shared
// runner (E6, runner.disk in the bench config): the janitor over this
// runner's own store, image GC of Heimdall images, the build cache prune of
// Heimdall's own builder, and the free disk floor checked before every
// lease. On a Kubernetes runner there is no local Docker to sweep, so only
// the disk floor applies.
func newRunnerHygiene(a *app) *engine.Hygiene {
	d := a.cfg.Runner.Disk
	opts := engine.HygieneOptions{StoreRoot: a.store.Root(), Every: d.JanitorEvery(), MinFreeBytes: d.MinFreeBytes(), Logger: a.logger}
	if a.cfg.Environment.DriverName() != "docker" {
		return engine.NewHygiene(nil, a.docker, opts)
	}
	opts.ImageMaxUnused = d.ImageMaxUnused()
	opts.BuildCacheKeep = d.BuildCacheKeepBytes()
	opts.Builder = strings.TrimSpace(d.Builder)
	if opts.Builder == "" {
		opts.Builder = strings.TrimSpace(os.Getenv("BUILDX_BUILDER"))
	}
	opts.Pinned = append([]string(nil), d.PinnedImages...)
	for _, img := range []string{a.cfg.Images.Helper, a.cfg.Images.ToolProxy} { // trusted helpers every run needs
		if img != "" {
			opts.Pinned = append(opts.Pinned, img)
		}
	}
	return engine.NewHygiene(engine.NewJanitor(a.reg.Gateway, a.docker, a.logger), a.docker, opts)
}
