package handlers

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// slowForecast answers like pricedForecast after a fixed delay per call, as
// a server forecast that reads the run store does.
type slowForecast struct {
	pricedForecast
	delay time.Duration
}

func (f *slowForecast) Estimate(ctx context.Context, exp *schemas.Experiment) (*schemas.Forecast, error) {
	time.Sleep(f.delay)
	return f.pricedForecast.Estimate(ctx, exp)
}

// TestEstimatePresetsConcurrent: the three presets are estimated side by
// side, not one after another (D4.slow), and keep their order.
func TestEstimatePresetsConcurrent(t *testing.T) {
	src := &slowForecast{pricedForecast: pricedForecast{priced: map[string]bool{"gpt-x": true}}, delay: 200 * time.Millisecond}
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"plain"}}
	if err := normalizeEvalRequest(req); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out := estimatePresets(context.Background(), estimateSuites(), src, req, nil, "", nil)
	took := time.Since(start)
	if took >= 450*time.Millisecond {
		t.Fatalf("three presets took %v, want under 450ms (side by side)", took)
	}
	var ids []string
	for _, p := range out {
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{PresetQuick, PresetStandard, PresetThorough}) {
		t.Fatalf("ids %v", ids)
	}
	if src.calls() != 3 {
		t.Fatalf("forecast calls %d, want 3", src.calls())
	}
	// The caller's request is not changed by the presets.
	if req.TasksPerBenchmark != EvalDefaultTasks || req.Trials != EvalDefaultTrials || len(req.Faults) != 0 {
		t.Fatalf("request changed: %+v", req)
	}
}
