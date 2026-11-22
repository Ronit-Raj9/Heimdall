package bifrostfeatures

import (
	"math"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func cacheRow(lat float64, hit bool, usd float64) schemas.GatewayRequest {
	return schemas.GatewayRequest{Kind: "message", LatencyMS: lat, CacheHit: hit, BifrostUSD: &usd}
}

func cacheRun(id, task, arm, cond string, ok bool, rows ...schemas.GatewayRequest) CacheRun {
	return CacheRun{RunRows: RunRows{RunID: id, Task: task, Arm: arm, Condition: cond, Rows: rows}, Success: &ok}
}

func TestCacheABSavings(t *testing.T) {
	runs := []CacheRun{
		cacheRun("r1", "t1", "A", "clean", true, cacheRow(1000, false, 0.01), cacheRow(1000, false, 0.01)),
		cacheRun("r2", "t2", "A", "clean", true, cacheRow(1000, false, 0.01), cacheRow(1000, false, 0.01)),
		// cache on: half the requests hit, a hit costs nothing and is fast
		cacheRun("r3", "t1", "cache", "clean", true, cacheRow(1000, false, 0.01), cacheRow(10, true, 0)),
		cacheRun("r4", "t2", "cache", "clean", false, cacheRow(1000, false, 0.01), cacheRow(10, true, 0)),
		// faulted and one-sided runs never count
		cacheRun("r5", "t1", "cache", "c-model-down", false, cacheRow(5, true, 0)),
		cacheRun("r6", "t3", "A", "clean", false, cacheRow(9000, false, 1)),
	}
	rep := CacheAB(runs, "A", "cache")
	if rep.Tasks != 2 || rep.Off.Runs != 2 || rep.On.Runs != 2 {
		t.Fatalf("tasks/runs: %+v", rep)
	}
	if rep.On.CacheHits != 2 || rep.On.HitRate != 0.5 || rep.Off.HitRate != 0 {
		t.Fatalf("hits: %+v", rep.On)
	}
	if math.Abs(rep.LatencyDeltaMS-(505-1000)) > 1e-9 {
		t.Fatalf("latency delta %v", rep.LatencyDeltaMS)
	}
	if math.Abs(rep.CostSavedUSD-0.02) > 1e-9 {
		t.Fatalf("cost saved %v", rep.CostSavedUSD)
	}
	if rep.SuccessDelta != -0.5 {
		t.Fatalf("success delta %v", rep.SuccessDelta)
	}
	for _, want := range []string{"50% of model requests", "saved $0.0200", "cut mean latency by 495 ms", "Success fell by 50%"} {
		if !strings.Contains(rep.Summary, want) {
			t.Fatalf("summary %q lacks %q", rep.Summary, want)
		}
	}
}

func TestCacheABNoHitsAndNoOverlap(t *testing.T) {
	if got := CacheAB(nil, "A", "cache").Summary; !strings.Contains(got, "No task ran") {
		t.Fatalf("empty: %q", got)
	}
	runs := []CacheRun{
		cacheRun("r1", "t1", "A", "clean", true, cacheRow(100, false, 0.01)),
		cacheRun("r2", "t1", "cache", "clean", true, cacheRow(100, false, 0.01)),
	}
	rep := CacheAB(runs, "A", "cache")
	if !strings.Contains(rep.Summary, "no cache hits") || rep.CostSavedUSD != 0 {
		t.Fatalf("no hits: %+v", rep)
	}
}

func TestCacheABRunCostWins(t *testing.T) {
	c := 0.5
	r := cacheRun("r1", "t1", "A", "", true, cacheRow(100, false, 0.01))
	r.CostUSD = &c
	st := armStats([]CacheRun{r}, "A", map[string]bool{"t1": true})
	if st.CostUSD != 0.5 || st.CostRuns != 1 {
		t.Fatalf("run cost: %+v", st)
	}
}
