package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/validity"
)

// W4-49 E: the stale scan. For each experiment it compares the gate
// versions of up to three sealed runs with the versions the gates have now;
// any difference lists the experiment as "could change if re-scored". No
// model is called and nothing is spent.

const kindValidityScan = "validity.scan"

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindValidityScan, Local: true, Hosted: true,
		Opts: []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: func(_ context.Context, _ jobEnv, raw json.RawMessage) (any, string, error) {
			var p struct{}
			if len(raw) > 0 && string(raw) != "null" {
				if err := strictParams(raw, &p); err != nil {
					return nil, "", err
				}
			}
			return p, "", nil
		},
		Run: runValidityScanJob,
	})
}

func runValidityScanJob(ctx context.Context, env jobEnv, _ *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	if env.App == nil || env.App.reg == nil {
		return nil, errors.New("this server has no gate registry")
	}
	idx := env.Services.Index
	if idx == nil {
		return nil, errors.New("this server has no run index")
	}
	stale := validityScan(ctx, env.App.reg, idx, time.Now())
	if err := validity.Open(env.ProjectRoot).SetStale(stale); err != nil {
		return nil, err
	}
	log.Printf("%d comparisons could change if re-scored", len(stale))
	return map[string]any{"stale": len(stale)}, nil
}

// validityScan lists the experiments whose sealed gate versions differ from
// the registry's current ones.
func validityScan(ctx context.Context, reg *schemas.Registry, idx handlers.ReadIndex, now time.Time) []validity.Stale {
	out := []validity.Stale{}
	ids := idx.Experiments()
	sort.Strings(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		m, ok := idx.Manifest(id)
		if !ok {
			continue
		}
		current := engine.RegateVersions(reg, m)
		var sealed [][]schemas.GateResult
		for _, rv := range idx.Runs(id) {
			if len(sealed) >= 3 {
				break
			}
			if !rv.Sealed {
				continue
			}
			b, err := idx.RunFile(rv.Run.RunID, handlers.FileGates)
			if err != nil {
				continue
			}
			var gates []schemas.GateResult
			if json.Unmarshal(b, &gates) == nil {
				sealed = append(sealed, gates)
			}
		}
		if s, ok := validity.StaleOf(id, current, sealed, now); ok {
			out = append(out, s)
		}
	}
	return out
}
