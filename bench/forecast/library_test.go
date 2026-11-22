package forecast

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLibraryEstimate(t *testing.T) {
	m := plannedManifest(1, 1, 0) // arms A (m-a) and B (m-b), task t1, no history of either
	lib := []HistoryRun{
		hist("e9", "other", "claude-code", "m-a", "t1", 1.0),
		hist("e9", "other", "claude-code", "m-a", "t1", 1.0),
		hist("e9", "other", "claude-code", "m-a", "t1", 1.0),
		hist("e9", "z", "mini", "unpriced", "t1", 50), // cannot be repriced: left out of cost
	}
	reprice := func(from, to string) (float64, float64, float64, bool) {
		if from != "m-a" {
			return 0, 0, 0, false
		}
		return 2, 3, 4, true // to m-b
	}
	f := Estimate(Input{Manifest: m, Library: lib, Reprice: reprice, Concurrency: 1, Now: time.Unix(0, 0)})
	var a, b schemas.ArmForecast
	for _, x := range f.Arms {
		if x.Arm == "A" {
			a = x
		} else {
			b = x
		}
	}
	if a.Basis != schemas.BasisLibrary || b.Basis != schemas.BasisLibrary || !LibraryBasis(a) {
		t.Fatalf("bases %s %s", a.Basis, b.Basis)
	}
	// Same model: $1 a run, widened by half each way.
	if a.CostUSD.Kind != schemas.RangeHistory || !near(*a.CostUSD.Low, 1/LibraryWiden) || !near(*a.CostUSD.High, LibraryWiden) || !near(*a.CostUSD.Median, 1) {
		t.Fatalf("arm A cost %+v", a.CostUSD)
	}
	// Repriced to m-b: low x2, mid x3, high x4, then widened.
	if !near(*b.CostUSD.Low, 2/LibraryWiden) || !near(*b.CostUSD.Median, 3) || !near(*b.CostUSD.High, 4*LibraryWiden) {
		t.Fatalf("arm B cost %+v", b.CostUSD)
	}
	// Time reads every library run, the unpriced one too.
	if a.AgentTimeS.Kind != schemas.RangeHistory || a.HistoryRuns != 4 {
		t.Fatalf("arm A time %+v runs %d", a.AgentTimeS, a.HistoryRuns)
	}
	if !strings.HasPrefix(a.BasisText, "first run, estimate from library data") || !strings.Contains(a.BasisText, "claude-code") {
		t.Fatalf("basis text %q", a.BasisText)
	}
	if !strings.Contains(f.Basis, "library runs") {
		t.Fatalf("summary %q", f.Basis)
	}
}

func TestLibraryNeverBeatsOwnHistory(t *testing.T) {
	m := plannedManifest(1, 1, 0)
	own := []HistoryRun{hist("e1", "hashA", "codex", "m-a", "t1", 0.1)}
	lib := []HistoryRun{hist("e9", "x", "claude-code", "m-a", "t1", 5)}
	f := Estimate(Input{Manifest: m, History: own, Library: lib, Now: time.Unix(0, 0)})
	for _, a := range f.Arms {
		switch {
		case a.Arm == "A" && a.Basis != schemas.BasisConfigHash:
			t.Fatalf("arm A basis %s, want its own history", a.Basis)
		case a.Arm == "B" && a.Basis != schemas.BasisLibrary:
			t.Fatalf("arm B basis %s, want library", a.Basis)
		}
	}
}

func TestLibraryUnusable(t *testing.T) {
	m := plannedManifest(1, 1, 0)
	t.Run("other suite", func(t *testing.T) {
		r := hist("e9", "x", "claude-code", "m-a", "t1", 1)
		r.Suite = "other@1"
		f := Estimate(Input{Manifest: m, Library: []HistoryRun{r}, Now: time.Unix(0, 0)})
		for _, a := range f.Arms {
			if a.Basis == schemas.BasisLibrary {
				t.Fatalf("arm %s used another suite's runs", a.Arm)
			}
		}
	})
	t.Run("time known, cost not repriceable", func(t *testing.T) {
		r := hist("e9", "x", "claude-code", "unpriced", "t1", 1)
		f := Estimate(Input{Manifest: m, Library: []HistoryRun{r}, Now: time.Unix(0, 0)})
		for _, a := range f.Arms {
			if a.Basis != schemas.BasisLibrary || a.CostUSD.Kind != schemas.RangeUnknown || a.AgentTimeS.Kind != schemas.RangeHistory {
				t.Fatalf("arm %s %+v", a.Arm, a)
			}
		}
	})
}
