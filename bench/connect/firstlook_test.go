package connect

import (
	"strings"
	"testing"
)

// B8.task: a practice benchmark (the Acme examples, coding-core) is never
// the first look while a real benchmark fits; it is only the fallback.
func TestFirstLookSkipsPractice(t *testing.T) {
	infos := []BenchmarkInfo{
		{ID: "acme-memory", Compatible: true, Tasks: 4, EstimateS: 10, EstimateBasis: "time limit", Practice: true},
		{ID: "coding-core", Compatible: true, Tasks: 3, EstimateS: 20, EstimateBasis: "time limit", Practice: true},
		{ID: "gaia", Compatible: true, Tasks: 30, EstimateS: 120, EstimateBasis: "time limit"},
	}
	p, err := PickFirstLook(infos, true)
	if err != nil || p.Benchmark != "gaia" || p.Practice {
		t.Fatalf("real benchmark first: %+v %v", p, err)
	}
	for _, c := range p.Considered {
		if c.Practice {
			t.Fatalf("practice benchmark considered next to a real one: %+v", p.Considered)
		}
	}
	p, err = PickFirstLook(infos[:2], true)
	if err != nil || p.Benchmark != "acme-memory" || !p.Practice || !strings.Contains(p.Reason, "practice benchmark") {
		t.Fatalf("practice fallback: %+v %v", p, err)
	}
	// An incompatible real benchmark does not block the fallback.
	p, err = PickFirstLook(append([]BenchmarkInfo{{ID: "swe", Tasks: 50}}, infos[:2]...), false)
	if err != nil || p.Benchmark != "acme-memory" || !p.Practice {
		t.Fatalf("practice fallback next to an incompatible benchmark: %+v %v", p, err)
	}
}
