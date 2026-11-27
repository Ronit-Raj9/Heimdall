package handlers

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// firstLookCost estimates the first look ev the way POST
// /evaluations/estimate would; nil when it cannot be estimated.
func (h *connectHandler) firstLookCost(ctx context.Context, ev schemas.EvaluationSpec) *EvalCostRange {
	if h.env.Services.Suites == nil {
		return nil
	}
	req := EvalEstimateRequest{Name: ev.Name, Mode: ev.Mode, Benchmarks: ev.Benchmarks, TasksPerBenchmark: ev.TasksPerBenchmark,
		Trials: ev.Trials, Faults: ev.Faults, Concurrency: ev.Concurrency}
	for _, a := range ev.Arms {
		req.Arms = append(req.Arms, EvalEstimateArm{Label: a.Label, Agent: a.Agent, Version: a.Version, Model: a.Model, Provider: a.Provider})
	}
	root := ""
	if p := h.env.Services.Projects; p != nil {
		root = p.Root()
	} else if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	var src ForecastSource
	if root != "" {
		src = quickRunForecast(root)
	}
	agents := (&evaluationsHandler{env: h.env}).agentConfigs(ctx)
	est, err := EstimateEvaluationWith(ctx, h.env.Services.Suites, src, &req, agents, root)
	if err != nil || est == nil {
		return nil
	}
	c := est.EstCostUSD
	return &c
}
