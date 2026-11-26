package main

import (
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
)

// Judge cache defaults (W4 feature 7, D3 10.9).
const (
	judgeCacheTTL        = 30 * 24 * time.Hour
	judgeCacheMaxEntries = 200000
)

// judgeCache is the judge cache of an app: a file cache under the org
// root, or nil on the shared worker, which builds every org's app from one
// config root, so a file cache there would be shared across organizations.
func judgeCache(cfg *lib.Config, opts appOptions) graders.JudgeCache {
	if opts.sharedRunner || cfg == nil || cfg.Root == "" {
		return nil
	}
	return graders.NewFileJudgeCache(filepath.Join(cfg.Root, ".heimdall", "judgecache"), judgeCacheTTL, judgeCacheMaxEntries)
}
