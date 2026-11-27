package forecast

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAgentTokensEstimate(t *testing.T) {
	m := plannedManifest(2, 1, 0)                       // arms A (m-a) and B (m-b), agent codex, two runs each
	other := hist("e8", "x", "codex", "m-z", "q1", 1.0) // 1e6 tokens, 1000 s
	other.Suite = "other@1"
	stranger := hist("e8", "y", "mini", "m-z", "q1", 5.0)
	stranger.Suite = "other@1"
	price := func(model string) (float64, float64, float64, bool) {
		if model == "m-a" {
			return 1e-6, 2e-6, 3e-6, true
		}
		return 0, 0, 0, false
	}
	f := Estimate(Input{Manifest: m, AgentRuns: []HistoryRun{other, other, stranger}, TokenPrice: price, Concurrency: 1, Now: time.Unix(0, 0)})
	var a, b schemas.ArmForecast
	for _, x := range f.Arms {
		if x.Arm == "A" {
			a = x
		} else {
			b = x
		}
	}
	if a.Basis != BasisAgentTokens || a.HistoryRuns != 2 {
		t.Fatalf("arm A basis %s runs %d", a.Basis, a.HistoryRuns)
	}
	// 1e6 tokens a run at 1e-6, 2e-6, 3e-6 a token, two runs, widened.
	if a.CostUSD.Kind != schemas.RangeHistory || !near(*a.CostUSD.Low, 2/LibraryWiden) || !near(*a.CostUSD.Median, 4) || !near(*a.CostUSD.High, 6*LibraryWiden) {
		t.Fatalf("arm A cost %+v", a.CostUSD)
	}
	if !near(*a.AgentTimeS.Median, 2000) || !strings.HasPrefix(a.BasisText, "rough:") {
		t.Fatalf("arm A time %+v text %q", a.AgentTimeS, a.BasisText)
	}
	// m-b has no list price: the old answer stays.
	if b.Basis != schemas.BasisNone {
		t.Fatalf("arm B basis %s", b.Basis)
	}
	if !strings.Contains(f.Basis, "rough list-price estimate") {
		t.Fatalf("summary %q", f.Basis)
	}
}

func TestAgentTokensOffByDefault(t *testing.T) {
	m := plannedManifest(1, 1, 0)
	f := Estimate(Input{Manifest: m, AgentRuns: []HistoryRun{hist("e8", "x", "codex", "m-a", "q1", 1)}, Concurrency: 1})
	for _, a := range f.Arms {
		if a.Basis == BasisAgentTokens {
			t.Fatalf("estimate without TokenPrice: %+v", a)
		}
	}
}
