package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/suite"
)

// newRunnerHygiene is the disk upkeep of `runner start` and the shared
// runner (E6, runner.disk in the bench config): the janitor over this
// runner's own store, image GC of Heimdall images, the build cache prune of
// Heimdall's own builder, and the free disk floor checked before every
// lease. On a Kubernetes runner there is no local Docker to sweep, so only
// the disk floor applies.
func newRunnerHygiene(a *app, projectDirs ...string) *engine.Hygiene {
	d := a.cfg.Runner.Disk
	opts := engine.HygieneOptions{StoreRoot: a.store.Root(), Every: d.JanitorEvery(), MinFreeBytes: d.MinFreeBytes(), Logger: a.logger}
	if a.cfg.Environment.DriverName() != "docker" {
		return engine.NewHygiene(nil, a.docker, opts)
	}
	if d.ImageGCOn(os.Getenv(suite.LibraryEnv)) {
		opts.ImageMaxUnused = d.ImageMaxUnused()
	} else {
		a.logger.Info("runner image cleanup off: a benchmark library shares this host (runner.disk.image_gc auto); set it to on to clean up anyway")
	}
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
	keeper := &engine.SuiteImageKeeper{Roots: func() ([]string, error) { return runnerBenchmarkRoots(a, projectDirs...) }, Extra: opts.Pinned}
	opts.Keep = keeper.KeepSet // never an image a suite here needs (library, org projects, this checkout)
	return engine.NewHygiene(engine.NewJanitor(a.reg.Gateway, a.docker, a.logger), a.docker, opts)
}

// runnerBenchmarkRoots are every benchmarks folder whose suites this runner
// may run: its own checkout's, the built-in library's, and each org
// project's under projectDirs (the shared runner's downloaded org projects).
func runnerBenchmarkRoots(a *app, projectDirs ...string) ([]string, error) {
	roots := []string{a.cfg.Path(a.cfg.BenchmarksRoot), suite.LibraryBenchmarks(suite.LibraryDir())}
	for _, dir := range projectDirs {
		if dir = strings.TrimSpace(dir); dir == "" {
			continue
		}
		orgs, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read org projects %s: %w", dir, err)
		}
		for _, o := range orgs {
			roots = append(roots, filepath.Join(dir, o.Name(), "benchmarks"))
		}
	}
	return roots, nil
}
