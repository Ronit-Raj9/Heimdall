package reportcard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestReliabilityAndResilienceCards(t *testing.T) {
	src := newSource()
	c := func(cost float64) map[string]float64 { return map[string]float64{metricCostUSD: cost} }
	var runs []fixRun
	// A clean: t1 always passes, t2 flips, t3 always fails, t4 passes. 3 trials.
	for trial := 1; trial <= 3; trial++ {
		runs = append(runs, pass("A", "t1", trial, c(1)), fail("A", "t3", trial, c(1)), pass("A", "t4", trial, c(1)))
		if trial == 1 {
			runs = append(runs, pass("A", "t2", trial, c(1)))
		} else {
			runs = append(runs, fail("A", "t2", trial, c(1)))
		}
		// B clean has no cost metric: cost per solved stays unknown.
		runs = append(runs, pass("B", "t1", trial, nil), pass("B", "t2", trial, nil))
	}
	// tool-rate-limit on A: t1 passes at double cost, t4 fails; metrics present.
	runs = append(runs,
		fixRun{arm: "A", task: "t1", cond: "tool-rate-limit", outcome: schemas.OutcomePass,
			metrics: map[string]float64{metricCostUSD: 2, metricRecovered: 1, metricTimeToRecover: 4, metricRetryAfterRespected: 1}},
		fixRun{arm: "A", task: "t4", cond: "tool-rate-limit", outcome: schemas.OutcomeFail,
			metrics: map[string]float64{metricCostUSD: 2, metricRecovered: 0, metricRetryAfterRespected: 0}},
		// model-slow on B without any fault metrics.
		fixRun{arm: "B", task: "t1", cond: "model-slow", outcome: schemas.OutcomePass},
	)
	src.add("exp", "hd-harbor-tb2", []string{"A", "B"}, runs)
	ev := abEval(schemas.EvaluationChild{Benchmark: "hd-harbor-tb2", ExperimentID: "exp", Status: "done"})
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	card := rep.Benchmarks[0].ReliabilityCard["A"]
	if card == nil || card.K != 3 || card.Tasks != 4 || len(card.Curve) != 3 {
		t.Fatalf("card %+v", card)
	}
	if *card.Pass1 != round(7.0/12) || *card.PassAtK != 0.75 || *card.PassHatK != 0.5 {
		t.Fatalf("pass numbers %v %v %v", *card.Pass1, *card.PassAtK, *card.PassHatK)
	}
	if *card.FlipRate != 0.25 || *card.CostPerSolved != round(12.0/7) {
		t.Fatalf("flip %v cost %v", *card.FlipRate, *card.CostPerSolved)
	}
	// consistency: (1 + 1/9 + 1 + 1)/4
	if *card.Consistency != round((3+1.0/9)/4) {
		t.Fatalf("consistency %v", *card.Consistency)
	}
	if b := rep.Benchmarks[0].ReliabilityCard["B"]; b == nil || b.CostPerSolved != nil || *b.FlipRate != 0 {
		t.Fatalf("B card %+v", b)
	}

	if len(rep.Resilience) != 2 || rep.Resilience[0].Fault != "model-slow" || rep.Resilience[1].Fault != "tool-rate-limit" {
		t.Fatalf("resilience %+v", rep.Resilience)
	}
	ra := rep.Resilience[1].Arms["A"]
	if ra.Tasks != 2 || *ra.RecoveryRate != 0.5 || *ra.DegradationRatio != 0.5 || *ra.CostInflation != 2 {
		t.Fatalf("A resilience %+v", ra)
	}
	if ra.DegradationLow == nil || *ra.DegradationLow > 0.5 || *ra.DegradationHigh < 0.5 {
		t.Fatalf("degradation band %+v", ra)
	}
	if *ra.TimeToRecoverS != 4 || *ra.RetryAfterRespected != 0.5 || *ra.RecoveredShare != 0.5 {
		t.Fatalf("fault metrics %+v", ra)
	}
	rb := rep.Resilience[0].Arms["B"]
	if rb.TimeToRecoverS != nil || rb.RetryAfterRespected != nil || rb.CostInflation != nil || *rb.RecoveryRate != 1 {
		t.Fatalf("B resilience unknowns should be nil: %+v", rb)
	}
}

func TestDefaultFaultCatalogIDs(t *testing.T) {
	for _, id := range FaultIDs {
		if DefaultFault(id) != id {
			t.Errorf("DefaultFault(%q) = %q", id, DefaultFault(id))
		}
		if id != FaultMisleading && DefaultFault("heimdall-"+id) != id {
			t.Errorf("generic probe of %q maps to %q", id, DefaultFault("heimdall-"+id))
		}
	}
	// No guessing from a probe's name (X06): only fault ids and their
	// generic probes map without benchmark metadata.
	if DefaultFault("outage-model-down") != "" || DefaultFault("clean") != "" || DefaultFault("important_instructions") != "" {
		t.Fatal("DefaultFault guessed a fault from a name")
	}
}

// pairedFaultFixture is the live BFCL case of N57 and N58: 10 tasks with one
// trial each, the clean run of t9 is an infrastructure error, and every
// fault run has the same outcome as its clean run except t9, which passes.
func pairedFaultFixture() []fixRun {
	var runs []fixRun
	for i := 0; i < 10; i++ {
		task := fmt.Sprintf("t%d", i)
		outcome := schemas.OutcomeFail
		if i < 5 {
			outcome = schemas.OutcomePass
		}
		clean := outcome
		if i == 9 {
			clean, outcome = schemas.OutcomeAbortedInfra, schemas.OutcomePass
		}
		runs = append(runs,
			fixRun{arm: "A", task: task, outcome: clean},
			fixRun{arm: "A", task: task, cond: FaultModelDown, outcome: outcome},
		)
	}
	return runs
}

func TestReliabilityPairsByTask(t *testing.T) {
	src := newSource()
	src.add("exp", "hd-harbor-bfcl", []string{"A"}, pairedFaultFixture())
	ev := abEval(schemas.EvaluationChild{Benchmark: "hd-harbor-bfcl", ExperimentID: "exp", Status: "done"})
	ev.Spec.Arms = ev.Spec.Arms[:1]
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Reliability) != 1 || rep.Reliability[0].Fault != FaultModelDown {
		t.Fatalf("reliability %+v", rep.Reliability)
	}
	a := rep.Reliability[0].Arms["A"]
	if a.Drop != 0 || a.Score != round(5.0/9) || a.Runs != 9 || a.Unpaired != 1 {
		t.Fatalf("fault score %+v, want score 5/9, change 0, 9 runs, 1 unpaired", a)
	}
}

func TestResilienceNoChangedTaskHasNoRange(t *testing.T) {
	src := newSource()
	src.add("exp", "hd-harbor-bfcl", []string{"A"}, pairedFaultFixture())
	ev := abEval(schemas.EvaluationChild{Benchmark: "hd-harbor-bfcl", ExperimentID: "exp", Status: "done"})
	ev.Spec.Arms = ev.Spec.Arms[:1]
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Resilience) != 1 {
		t.Fatalf("resilience %+v", rep.Resilience)
	}
	a := rep.Resilience[0].Arms["A"]
	if a.Tasks != 9 || a.DegradationRatio == nil || *a.DegradationRatio != 1 || a.ChangedTasks != 0 {
		t.Fatalf("resilience arm %+v", a)
	}
	if a.DegradationLow != nil || a.DegradationHigh != nil {
		t.Fatalf("no task changed its result, so there is no range: %v to %v", *a.DegradationLow, *a.DegradationHigh)
	}
	b, _ := json.Marshal(a)
	if !strings.Contains(string(b), `"changed_tasks":0`) {
		t.Fatalf("changed_tasks must be sent even when 0: %s", b)
	}
}
