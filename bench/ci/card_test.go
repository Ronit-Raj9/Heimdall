package ci

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func f64(v float64) *float64 { return &v }

// cardAnalysis is a regressed verdict with a policy block, cost on both
// arms and four differing tasks.
func cardAnalysis() *schemas.Analysis {
	p := 0.031
	return &schemas.Analysis{
		AnalysisID: "a_golden1", ExpID: "e_compare-golden", Mode: schemas.ModeLive,
		Verdict: schemas.Verdict{
			Primary: schemas.Endpoint{Condition: "clean", Metric: "verified_success"}, Decision: schemas.DecisionRegressed, Action: schemas.ActionHold,
			Effect: &schemas.Effect{Condition: "clean", Metric: "verified_success", ArmA: "A", ArmB: "B", NTasks: 10, NNonzero: 4, NPositive: 0, NNegative: 4,
				DBar: -0.3, PValue: &p, Interval: &[2]float64{-0.52, -0.08},
				PerTask: []schemas.TaskDiff{
					{Task: "m-1", MeanA: 1, MeanB: 0, D: -1}, {Task: "m-2", MeanA: 1, MeanB: 1, D: 0},
					{Task: "m-3", MeanA: 1, MeanB: 0.67, D: -0.33}, {Task: "m-4", MeanA: 0.67, MeanB: 0, D: -0.67},
					{Task: "m-5", MeanA: 1, MeanB: 0.67, D: -0.33},
				}},
			Integrity: schemas.IntegrityClean,
		},
		Naive: map[string]schemas.Ratio{"A": {Num: 28, Den: 30}, "B": {Num: 19, Den: 30, Unknown: 1}},
		Diagnostics: []schemas.DiagnosticsRow{
			{Condition: "clean", PerArm: map[string]schemas.Ratio{"A": {Num: 28, Den: 30}, "B": {Num: 19, Den: 30, Unknown: 1}}},
		},
		Meters: []schemas.Meter{{Name: "cost_per_verified_success", PerArm: map[string]schemas.Value{
			"A": {State: "known", V: f64(0.0412)}, "B": {State: "unknown", Reason: "usage missing"},
		}}},
		Policy: &schemas.PolicyResult{PolicyName: "enterprise-default", Decision: schemas.PolicyBlock, Checks: []schemas.PolicyCheckResult{
			{ID: "clean-no-regression", Status: schemas.PolicyFail, Blocking: true}, {ID: "no-integrity-violations", Status: schemas.PolicyPass},
		}},
	}
}

func TestVerdictCardGolden(t *testing.T) {
	tests := []struct {
		name string
		an   func() *schemas.Analysis
		in   CardInput
	}{
		{"regressed", cardAnalysis, CardInput{
			ArmLabels: map[string]string{"A": "main (1a2b3c)", "B": "HEAD | candidate"}, Unreviewed: 3, Tasks: 10,
			UILink: "http://127.0.0.1:8090/verdict?exp=e_compare-golden", Evidence: ".heimdall/runs/e_compare-golden",
			Subject: "agent files at `main` vs `HEAD`",
		}},
		{"synthetic-invalid", func() *schemas.Analysis {
			an := cardAnalysis()
			an.Mode = schemas.ModeSynthetic
			an.Policy = nil
			an.Verdict.Decision, an.Verdict.Action, an.Verdict.Effect = schemas.DecisionInvalid, schemas.ActionFixHarness, nil
			an.Verdict.InvalidReasons = []string{"usage incomplete on arm B", "seal | mismatch <!-- x -->"}
			an.Verdict.Integrity = schemas.IntegrityAlert
			an.Meters = nil
			return an
		}, CardInput{Tasks: 4}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VerdictCard(tc.an(), tc.in)
			path := filepath.Join("testdata", "card-"+tc.name+".golden.md")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./ci/ -run Golden -update)", err)
			}
			if got != string(want) {
				t.Errorf("card differs from %s:\n--- got\n%s\n--- want\n%s", path, got, want)
			}
			if !strings.HasPrefix(got, CardMarker+"\n") || strings.Count(got, "<!--") != 1 {
				t.Error("the card must start with the marker and hold no other HTML comment")
			}
		})
	}
}

func TestTopTasks(t *testing.T) {
	e := cardAnalysis().Verdict.Effect
	top := topTasks(e, 3)
	if len(top) != 3 || top[0].Task != "m-1" || top[1].Task != "m-4" || top[2].Task != "m-3" {
		t.Errorf("top = %+v", top)
	}
	if topTasks(nil, 3) != nil {
		t.Error("nil effect")
	}
}

func TestGitHubWorkflowGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    WorkflowOptions
	}{
		{"recorded", WorkflowOptions{Mode: ModeRecorded, Source: "Ronit-Raj9/Heimdall", SourceRef: "main", DataMode: "synthetic"}},
		{"live", WorkflowOptions{Mode: ModeLive, Source: "Ronit-Raj9/Heimdall", SourceRef: "0123abc", Base: "agents/claude-code.yaml",
			Trials: 3, Faults: []string{"model-outage"}, BudgetUSD: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GitHubWorkflow(tc.o)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "workflow-"+tc.name+".golden.yml")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./ci/ -run Golden -update)", err)
			}
			if got != string(want) {
				t.Errorf("workflow differs from %s:\n%s", path, got)
			}
			var doc struct {
				On   map[string]any `yaml:"on"`
				Jobs map[string]struct {
					Steps []map[string]any `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal([]byte(got), &doc); err != nil || len(doc.Jobs["gate"].Steps) < 7 || doc.On["pull_request"] == nil {
				t.Errorf("workflow is not the expected YAML: %v (%d steps)", err, len(doc.Jobs["gate"].Steps))
			}
			for _, must := range []string{"uses: ./.heimdall-src/bench/ci", "pull-requests: write", "heimdall-evidence", CardMarker, "if: always()"} {
				if !strings.Contains(got, must) {
					t.Errorf("workflow lacks %q", must)
				}
			}
			for _, never := range []string{"secrets.", "echo $", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
				if strings.Contains(got, never) {
					t.Errorf("workflow contains %q", never)
				}
			}
		})
	}
	if _, err := GitHubWorkflow(WorkflowOptions{Mode: ModeLive, Source: "x/y", SourceRef: "main"}); err == nil {
		t.Error("live mode without a base agent accepted")
	}
	if _, err := GitHubWorkflow(WorkflowOptions{Mode: "nightly", Source: "x/y", SourceRef: "main"}); err == nil {
		t.Error("unknown mode accepted")
	}
}
