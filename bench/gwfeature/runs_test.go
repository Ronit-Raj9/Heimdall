package gwfeature

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func runIDs(runs []Run) []string {
	var out []string
	for _, r := range runs {
		out = append(out, r.RunID)
	}
	return out
}

func TestLoadRunsNewestAttemptOnly(t *testing.T) {
	src := newFakeSource()
	src.addRun(t, "e1", "r1a", "t1", "A", "clean", 0, 1, false, 1)
	src.addRun(t, "e1", "r1b", "t1", "A", "clean", 0, 2, true, 2)
	src.setFile("r1b", "gates.json", []byte(`[{"gate":"model_fidelity","passed":true}]`))
	runs, err := LoadRuns(src, "e1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := runIDs(runs); len(got) != 1 || got[0] != "r1b" {
		t.Fatalf("runs %v, want [r1b]", got)
	}
	r := runs[0]
	if r.Success == nil || !*r.Success || r.CostUSD == nil || *r.CostUSD != 2 || r.Task != "t1" || r.Arm != "A" {
		t.Fatalf("run fields: %+v", r)
	}
	if len(r.Gates) != 1 {
		t.Fatalf("gates: %+v", r.Gates)
	}
	if r.Evidence != nil {
		t.Fatal("missing evidence.json must leave Evidence nil")
	}
}

func TestLoadRunsSkipsAbortedInfraAndSuperseded(t *testing.T) {
	src := newFakeSource()
	src.addRun(t, "e1", "r1", "t1", "A", "clean", 0, 1, true, 1)
	src.addRun(t, "e1", "r2", "t2", "A", "clean", 0, 1, true, 1).Superseded = true
	src.addRun(t, "e1", "r3", "t3", "A", "clean", 0, 1, true, 1).Run.Outcome = schemas.OutcomeAbortedInfra
	src.addRun(t, "e1", "r4", "t4", "A", "clean", 0, 1, true, 1).Sealed = false
	runs, err := LoadRuns(src, "e1", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := runIDs(runs); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("runs %v, want [r1]", got)
	}
}

func TestLoadRunsCleanOnlyByDefault(t *testing.T) {
	src := newFakeSource()
	src.addRun(t, "e1", "r2", "t1", "A", "probe1", 0, 1, true, 1)
	src.addRun(t, "e1", "r1", "t1", "A", "clean", 0, 1, true, 1)
	runs, _ := LoadRuns(src, "e1", false)
	if got := runIDs(runs); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("clean only: %v", got)
	}
	runs, _ = LoadRuns(src, "e1", true)
	if got := runIDs(runs); len(got) != 2 || got[0] != "r1" || got[1] != "r2" {
		t.Fatalf("all conditions sorted: %v", got)
	}
}
