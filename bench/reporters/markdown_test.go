package reporters

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testAnalysis is a recorded analysis with an unknown p-value on a guard, an
// unknown meter cell and an invalid-free verdict.
func testAnalysis() *schemas.Analysis {
	p := 0.015625
	frozen := time.Date(2026, 11, 14, 23, 41, 0, 0, time.UTC)
	return &schemas.Analysis{
		AnalysisID: "a_1", ExpID: "e_codewins", CreatedAt: frozen.Add(2 * time.Hour), GradeSource: "original",
		Mode: schemas.ModeRecorded,
		Verdict: schemas.Verdict{
			Primary:  schemas.Endpoint{Condition: "stale-notes", Metric: "verified_success"},
			Decision: schemas.DecisionImproved, Action: schemas.ActionShip,
			Effect: &schemas.Effect{DBar: 1.0 / 3, PValue: &p, NTasks: 8, NNonzero: 7,
				LowerBound: &schemas.Effect{DBar: 0.25}, UpperBound: &schemas.Effect{DBar: 0.375}},
			Alpha: 0.05, MDE: 34.6, MDEAssumedSD: true, SD: 0.3, RandomizationUnit: "task", TieRule: "zero differences dropped",
			Guards: []schemas.GuardResult{
				{Spec: schemas.GuardSpec{Condition: "clean", Rule: "no_regression"}, Status: schemas.GuardPass,
					Effect: &schemas.Effect{DBar: 0.0417, NTasks: 8, NNonzero: 2}},
				{Spec: schemas.GuardSpec{Rule: "no_new_integrity_violation"}, Status: schemas.GuardPass, Detail: "0 | 0 runs"},
			},
			Integrity: schemas.IntegrityClean, ManifestHash: "sha256:9f2ce1", FrozenAt: frozen,
		},
		Naive: map[string]schemas.Ratio{"A": {Num: 11, Den: 24}, "B": {Num: 19, Den: 24}},
		Diagnostics: []schemas.DiagnosticsRow{
			{Condition: "clean", Axis: "baseline", NTasks: 8, PerArm: map[string]schemas.Ratio{"A": {Num: 17, Den: 24}, "B": {Num: 18, Den: 24}},
				Effect: &schemas.Effect{DBar: 0.0417}},
			{Condition: "model-outage", Axis: "resilience", NTasks: 3, Descriptive: true,
				PerArm: map[string]schemas.Ratio{"A": {Num: 2, Den: 6}, "B": {Num: 4, Den: 5, Unknown: 1}}},
		},
		Meters: []schemas.Meter{
			{Name: "false_completion", Label: "honesty", Kind: schemas.KindRate, Numerator: "claims among failures", Denominator: "failures",
				PerArm: map[string]schemas.Value{"A": schemas.Unknown("no_claims", schemas.EvidenceNative), "B": schemas.Observed(0.4, schemas.EvidenceNative)}},
			{Name: "cost_per_success", Label: "efficiency", Kind: schemas.KindUSD, Numerator: "spend", Denominator: "verified successes",
				PerArm: map[string]schemas.Value{"A": schemas.Observed(0.01234, schemas.EvidenceGateway), "B": schemas.NotApplicable()}},
		},
		Policy: testPolicy(frozen),
	}
}

// testPolicy is a blocking policy result with a pass, a blocking fail with
// evidence, an unknown and a warn-only fail on a not-applicable value.
func testPolicy(at time.Time) *schemas.PolicyResult {
	return &schemas.PolicyResult{
		PolicyName: "enterprise-default", PolicyHash: "4f1c2a9b7d3e5f60718293a4b5c6d7e8", Decision: schemas.PolicyBlock, EvaluatedAt: at,
		Checks: []schemas.PolicyCheckResult{
			{ID: "clean-no-regression", Metric: "verified_success", Condition: "clean", Arm: "B", Status: schemas.PolicyPass,
				Observed: schemas.Observed(0.0417, schemas.EvidenceOutcome), Threshold: "no regression", N: 24,
				Reason: "clean: not regressed at either bound"},
			{ID: "false-completion-ceiling", Metric: "false_completion", Condition: "all", Arm: "B", Status: schemas.PolicyFail, Blocking: true,
				Observed: schemas.Observed(0.125, schemas.EvidenceNative), Threshold: "<= 0.05", N: 16, Reason: "mean 0.125 over 16 observed runs breaks <= 0.05",
				Evidence: []string{"r_1", "r_2", "r_3", "r_4", "r_5", "r_6", "r_7"}},
			{ID: "retry-amplification-ceiling", Metric: "retry_amplification", Condition: "clean", Arm: "B", Status: schemas.PolicyUnknown, Blocking: true,
				Observed: schemas.Unknown("not recorded", ""), Threshold: "<= 3", Reason: "metric retry_amplification is not recorded in any of the 24 runs | of arm B"},
			{ID: "cost-per-verified-success", Metric: "cost_per_verified_success", Condition: "clean", Arm: "B", Status: schemas.PolicyFail,
				Observed: schemas.NotApplicable(), Threshold: "<= 2", N: 24, Reason: "no verified success in 24 runs"},
		},
	}
}

func TestMarkdownGolden(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {}, "B": {}}}
	var buf bytes.Buffer
	if err := newMarkdownReporter().Write(context.Background(), testAnalysis(), m, &buf); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "report.golden.md")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if buf.String() != string(want) {
		t.Fatalf("report differs from %s:\n%s", golden, buf.String())
	}
}

func TestMarkdownRules(t *testing.T) {
	a := testAnalysis()
	a.Verdict.Effect.PValue = nil
	a.Verdict.MDE = 0
	a.Mode = schemas.ModeSynthetic
	var buf bytes.Buffer
	if err := newMarkdownReporter().Write(context.Background(), a, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	first, _, _ := strings.Cut(out, "\n")
	if first != "# Bifrost Bench: IMPROVED, action SHIP, policy BLOCK [SYNTHETIC]" {
		t.Errorf("first line = %q", first)
	}
	for _, want := range []string{"| p-value | unknown |", "| MDE | unknown |", "not findings", "| honesty | claims among failures / failures | unknown | 40.0% |"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(out, "| p-value | 0") {
		t.Error("unknown p-value written as 0")
	}
}

func TestMarkdownPolicy(t *testing.T) {
	render := func(a *schemas.Analysis) string {
		var buf bytes.Buffer
		if err := newMarkdownReporter().Write(context.Background(), a, nil, &buf); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	tests := []struct {
		name    string
		policy  *schemas.PolicyResult
		want    []string
		notWant []string
	}{
		{"no policy", nil, []string{"## Release policy\n\nNone.\n", "# Bifrost Bench: IMPROVED, action SHIP [RECORDED]"}, []string{"policy BLOCK"}},
		{"block", testPolicy(time.Time{}), []string{
			"Policy `enterprise-default`, hash `4f1c2a9b7d3e`: **BLOCK**.",
			"| false-completion-ceiling | false_completion | all | B | FAIL (blocks) | 0.125 | &lt;= 0.05 | 16 |",
			"`r_5` and 2 more |",
			"| retry-amplification-ceiling | retry_amplification | clean | B | UNKNOWN (blocks) | unknown | &lt;= 3 | 0 |",
			`24 runs \| of arm B`,
			"| cost-per-verified-success | cost_per_verified_success | clean | B | FAIL | n/a | &lt;= 2 | 24 |",
		}, []string{"UNKNOWN (blocks) | 0 |", "`r_6`"}},
		{"allow", &schemas.PolicyResult{PolicyName: "p", Decision: schemas.PolicyAllow}, []string{"**ALLOW**", "No checks.", "action SHIP, policy ALLOW"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testAnalysis()
			a.Policy = tt.policy
			out := render(a)
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("report lacks %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("report has %q", w)
				}
			}
		})
	}
}

func TestCell(t *testing.T) {
	if got := cell("a|b\n<script>"); got != `a\|b &lt;script&gt;` {
		t.Errorf("cell = %q", got)
	}
}
