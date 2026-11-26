package main

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/improve"
)

// Explain implements handlers.ImproveExplainer: what changed in version n
// of an improvement run and why (D5 A4.3). It reads only.
func (s improveSource) Explain(_ context.Context, id string, n int) (*improve.Explain, error) {
	return s.r.Explain(id, n)
}

func init() {
	handlers.OrgImproveSource = orgImproveExplainSource
}

// orgImproveExplainSource is a read-only improvement source over the
// organization's runs root, the folder its improvement runs and objects
// live in (newImproveRunner); it never starts or drives a run.
func orgImproveExplainSource(env handlers.OrgEnv) handlers.ImproveSource {
	if env.Cfg == nil {
		return nil
	}
	dir := filepath.Join(env.Cfg.Path(env.Cfg.RunsRoot), improve.DefaultDir)
	r := &improve.Runner{Root: dir, Objects: improve.Objects{Root: filepath.Join(dir, "objects")}}
	return improveSource{r: r, offline: true, mu: &sync.Mutex{}}
}
