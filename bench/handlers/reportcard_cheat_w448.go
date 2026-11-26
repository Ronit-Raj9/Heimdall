package handlers

import (
	"context"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// withCheatCheck gives a report card the lookups of its cheat rate row
// (W4-48, design D4 10.5): the evaluation's cheat check children and which
// benchmarks have admitted impossible twins. Both read lazily, once.
func (h *reportCardHandler) withCheatCheck(ctx context.Context, opt *reportcard.Options) {
	if h.evals == nil {
		return
	}
	root := (&evaluationsHandler{env: h.env}).root()
	var listed []schemas.Evaluation
	loaded := false
	opt.CheatChecks = func(id string) []*schemas.Evaluation {
		if !loaded {
			loaded = true
			listed, _ = h.evals.List(ctx)
		}
		var out []*schemas.Evaluation
		for i := range listed {
			if listed[i].CheatCheckOf == id {
				out = append(out, &listed[i])
			}
		}
		return out
	}
	twinsOf := map[string]bool{}
	opt.HasTwins = func(benchmark string) bool {
		ok, seen := twinsOf[benchmark]
		if !seen {
			ok = root != "" && len(twinBenchmarks(root, []string{benchmark})) > 0
			twinsOf[benchmark] = ok
		}
		return ok
	}
}
