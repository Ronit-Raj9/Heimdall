package reportcard

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/verdict"
)

// Fix wave 3, N200 with N215 and N257 (group B): in a score evaluation, one
// run served by a truly different model is thrown out alone by the real
// model_fidelity gate (1.1.0) and the real verdict policy: the other runs,
// including one served the requested alias as its dated snapshot, still
// count on the report card, and the thrown-out run is queued in Needs review
// as "invalidated" with the check named.
func TestDifferentModelRunThrownOutAlone(t *testing.T) {
	var fidelity schemas.Gate
	for _, g := range gates.Builtin() {
		if g.GetName() == gates.ModelFidelity {
			fidelity = g
		}
	}
	if fidelity == nil || fidelity.GetVersion() != "1.1.0" {
		t.Fatalf("model_fidelity gate %v", fidelity)
	}
	src := newGateSource()
	var fix []fixRun
	for i := range 8 {
		fix = append(fix, pass("A", fmt.Sprintf("t%d", i+1), 1, map[string]float64{verdict.MetricVerifiedSuccess: 1}))
	}
	src.add("e_one", "s", []string{"A"}, fix)
	src.manifests["e_one"].Experiment.Arms = map[string]schemas.ArmSpec{"A": {Label: "A"}} // a one-arm score experiment
	agent := &schemas.AgentConfig{Model: "gpt-5.4-mini"}
	served := map[int]string{0: "claude-haiku-4-5", 1: "gpt-5.4-mini-2026-03-17"} // run 0 another model, run 1 a dated snapshot
	zero := int64(0)
	var sealed []schemas.SealedRun
	for i, r := range src.runs["e_one"] {
		model := cmp.Or(served[i], "gpt-5.4-mini")
		res, err := fidelity.Check(context.Background(), &schemas.RunContext{Agent: agent},
			&schemas.Evidence{Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "q", Kind: "message", ModelRequested: "gpt-5.4-mini", ModelServed: model}}}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Fired != (i == 0) {
			t.Fatalf("run %d served %s: fired %v (%q)", i, model, res.Fired, res.Detail)
		}
		b, _ := json.Marshal([]schemas.GateResult{*res})
		src.gates[r.Run.RunID] = b
		sealed = append(sealed, schemas.SealedRun{Slot: r.Slot, Run: r.Run, GradeSource: "original", Gates: []schemas.GateResult{*res},
			Metrics: map[string]schemas.Value{verdict.MetricVerifiedSuccess: schemas.Observed(1, schemas.EvidenceOutcome)},
			Gateway: &schemas.GatewayTally{Requests: 1, WithUsage: 1, Complete: true, Dropped: &zero}})
	}

	// The verdict the server stores for this child: invalid only because
	// of the gate rate, so the scope is the runs.
	var policy schemas.VerdictPolicy
	for _, p := range verdict.Builtin() {
		if p.GetName() == verdict.PolicyPairedSignFlip {
			policy = p
		}
	}
	if policy == nil {
		t.Fatal("no paired sign-flip policy")
	}
	an, err := policy.Decide(context.Background(), src.manifests["e_one"], sealed)
	if err != nil {
		t.Fatal(err)
	}
	if an.Verdict.Decision != schemas.DecisionInvalid || an.Verdict.InvalidScope != "runs" {
		t.Fatalf("decision %s scope %q reasons %q, want invalid in scope runs", an.Verdict.Decision, an.Verdict.InvalidScope, an.Verdict.InvalidReasons)
	}
	src.decisions["e_one"] = an.Verdict.Decision
	src.scope["e_one"] = an.Verdict.InvalidScope
	src.reasons["e_one"] = an.Verdict.InvalidReasons

	rep, err := Build(context.Background(), src, scoreEval("done", schemas.EvaluationChild{Benchmark: "s", ExperimentID: "e_one", Status: "done"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := rep.Benchmarks[0]
	if b.Arms["A"].Runs != 7 || b.Arms["A"].Score != 1 {
		t.Fatalf("arm A row %+v, want the 7 other runs counted", b.Arms["A"])
	}
	if to := b.ThrownOut["A"]; to.Runs != 1 || to.Checks[gates.ModelFidelity] != 1 || rep.ThrownOutRuns != 1 {
		t.Fatalf("thrown out %+v, report total %d", b.ThrownOut, rep.ThrownOutRuns)
	}

	// Needs review: only the thrown-out run, with the check named.
	for i, sr := range sealed {
		reasons := store.QueueReasons(&sr, nil)
		if got := slices.Contains(reasons, schemas.ReviewReasonInvalidated); got != (i == 0) {
			t.Fatalf("run %d queue reasons %q", i, reasons)
		}
	}
	if by := store.ThrownOutBy(sealed[0].Gates); len(by) != 1 || by[0] != "model check: served by another model" {
		t.Fatalf("thrown out by %q", by)
	}
}
