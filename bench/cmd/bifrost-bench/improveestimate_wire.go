package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The improvement estimate (W4 design 5, A2.9): the handler's hook loads
// the spec's benchmark with the org's registry, partitions the pool as
// Create would and asks the org's forecast for the practice runs' cost.

var (
	improveEstimateAppsMu sync.Mutex
	improveEstimateApps   = map[string]*app{} // org project root -> app
)

func init() {
	serviceHooks = append(serviceHooks, func(a *app, _ *handlers.Services) {
		improveEstimateAppsMu.Lock()
		improveEstimateApps[a.cfg.Root] = a
		improveEstimateAppsMu.Unlock()
	})
	handlers.ImproveEstimateInputs = improveEstimateInputs
}

func improveEstimateInputs(ctx context.Context, spec *improve.Spec) (improve.EstimateInput, error) {
	env, ok := handlers.ImproveEstimateEnv(ctx)
	if !ok || env.Cfg == nil {
		return improve.EstimateInput{}, fmt.Errorf("estimates are not available on this server")
	}
	improveEstimateAppsMu.Lock()
	a := improveEstimateApps[env.Cfg.Root]
	improveEstimateAppsMu.Unlock()
	if a == nil {
		return improve.EstimateInput{}, fmt.Errorf("estimates are not available on this server")
	}
	s, err := engine.LoadSuite(ctx, a.reg, spec.Suite)
	if err != nil {
		return improve.EstimateInput{}, err
	}
	in, err := improveEstimatePool(s, spec)
	if err != nil {
		return improve.EstimateInput{}, err
	}
	agent, version := improveEstimateAgent(spec.Agent)
	req := &handlers.EvalEstimateRequest{Name: spec.Name, Arms: []handlers.EvalEstimateArm{{Label: "A", Agent: agent, Version: version}},
		Benchmarks: []string{spec.Suite}, TasksPerBenchmark: max(1, in.Splits.Practice), Trials: 1}
	if est, err := handlers.EstimateEvaluationWith(ctx, env.Services.Suites, serveForecast{a: a}, req, nil, env.Cfg.Root); err == nil {
		improveEstimateCost(&in, est)
	}
	return in, nil
}

// improveEstimatePool is the pool and split sizes Create would use: the
// suite's tasks (only those the spec lists, when it lists any), impossible
// tasks only as named honeypots.
func improveEstimatePool(s *schemas.Suite, spec *improve.Spec) (improve.EstimateInput, error) {
	pool := []string{}
	honeypots := 0
	for _, t := range s.Tasks {
		named := slices.Contains(spec.Splits.Honeypots, t.ID)
		if len(spec.Tasks) > 0 && !slices.Contains(spec.Tasks, t.ID) && !named {
			continue
		}
		if named {
			honeypots++
			continue
		}
		if t.Impossible {
			continue
		}
		pool = append(pool, t.ID)
	}
	sp, err := improve.Partition(pool, spec)
	if err != nil {
		return improve.EstimateInput{}, err
	}
	n := len(pool)
	if spec.MaxTasks > 0 {
		n = min(n, spec.MaxTasks)
	}
	return improve.EstimateInput{Spec: spec, Pool: n,
		Splits: improve.SplitCounts{Practice: len(sp.Dev), Gate: len(sp.Audit), HeldOut: len(sp.Sealed), Impossible: honeypots}}, nil
}

var improveAgentVersionRe = regexp.MustCompile(`^(.+)@v([0-9]+)$`)

// improveEstimateAgent splits agents/<id>.yaml@v<N> into the path and N.
func improveEstimateAgent(a string) (string, int) {
	if m := improveAgentVersionRe.FindStringSubmatch(a); m != nil {
		n, _ := strconv.Atoi(m[2])
		return m[1], n
	}
	return a, 0
}

// improveEstimateCost reads the per-run cost and minutes from an
// evaluation estimate of the practice runs. An unknown cost leaves the
// estimator to assume one.
func improveEstimateCost(in *improve.EstimateInput, est *handlers.EvalEstimate) {
	if est == nil || est.Runs <= 0 {
		return
	}
	n := float64(est.Runs)
	if est.EstCostUSD.Known {
		in.PerRunUSD = (est.EstCostUSD.Low + est.EstCostUSD.High) / 2 / n
		in.RunSource = "forecast"
	}
	if est.EstMinutes.Known {
		in.PerRunMin = (est.EstMinutes.Low + est.EstMinutes.High) / 2 / n
	}
}
