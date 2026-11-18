package monitorcert

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRenderMarkdown(t *testing.T) {
	rate, fpr, thr, score := 0.75, 0.01, 0.5, 0.9
	c := &schemas.MonitorCertificate{
		CertID: "mc_x", CreatedAt: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC), ExpIDs: []string{"e_a", "e_b"},
		Monitor: schemas.MonitorCertMonitor{Name: "kw", Kind: "builtin", Mode: "transcript", Network: "none", ConfigHash: "sha256:abc"},
		Labels: schemas.MonitorLabelSummary{Honest: 100, Attack: 4, Excluded: 2, Rule: "the rule",
			ExcludedReasons: map[string]int{"aborted_infra": 1, "veto under clean (no probe)": 1}},
		OperatingPoints: []schemas.MonitorOperatingPoint{
			{TargetFPR: 0.01, Supported: true, Threshold: &thr, FalsePositives: 1, Honest: 100, AchievedFPR: &fpr, FPRInterval: []float64{0.0003, 0.0545},
				Caught: 3, Attacks: 4, CatchRate: &rate, CatchInterval: []float64{0.194, 0.994}},
			{TargetFPR: 0.001, Honest: 100, FPRInterval: []float64{0, 1}, CatchInterval: []float64{0, 1}, Note: "a | b"},
		},
		Families: []schemas.MonitorFamily{{Family: "prompt-injection", Gates: []string{"g12_injection_followed"}, Attacks: 4,
			Points: []schemas.MonitorFamilyPoint{{TargetFPR: 0.01, Caught: 3, Attacks: 4, CatchRate: &rate, CatchInterval: []float64{0.194, 0.994}},
				{TargetFPR: 0.001, Attacks: 4, CatchInterval: []float64{0, 1}}}}},
		Warnings:    []string{"only 4 attack runs"},
		Items:       []schemas.MonitorItem{{RunID: "r_1", ExpID: "e_a", Task: "t01", Label: "attack", Score: &score, Decision: "block", SealHash: "sha256:0123456789abcdef"}},
		Experiments: []schemas.MonitorCertExperiment{{ExpID: "e_a", ManifestHash: "mh", SuiteID: "coding-core", SuiteVersion: "1.0.0", Runs: 5}},
		Integrity:   schemas.MonitorInjectionCheck{State: "passed", Detail: "changed none of 3"},
	}
	md := string(RenderMarkdown(c))
	for _, want := range []string{
		"# Monitor certificate mc_x", "Experiments: e_a, e_b", "Labels: 100 honest, 4 attack, 2 excluded",
		"| 0.01 | yes | 0.5 | 1 of 100 | 0.01 [0.0003, 0.0545] | 3 of 4 | 0.75 [0.194, 0.994] |  |",
		"| 0.001 | no | block only | 0 of 100 | undefined | 0 of 0 | undefined | a \\| b |",
		"| prompt-injection | g12_injection_followed | 4 | 3 of 4, 0.75 [0.194, 0.994] | 0 of 4, undefined |",
		"- only 4 attack runs", "Monitor-injection check: **passed**", "| aborted_infra | 1 |",
		"| e_a | coding-core@1.0.0 | `mh` | 5 |", "| r_1 | e_a | t01 |", "`0123456789ab`", "## Not covered",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.ContainsAny(md, "\u2013\u2014") {
		t.Error("report carries an en or em dash")
	}
}
