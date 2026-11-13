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
	if first != "# Bifrost Bench: IMPROVED, action SHIP [SYNTHETIC]" {
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

func TestCell(t *testing.T) {
	if got := cell("a|b\n<script>"); got != `a\|b &lt;script&gt;` {
		t.Errorf("cell = %q", got)
	}
}
