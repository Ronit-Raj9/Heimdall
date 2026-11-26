package improve

import (
	"slices"
	"strings"
	"testing"
)

const w4Good = "name: loop\nagent: agents/x.yaml\nsuite: s@1\neditable:\n  files: [/home/agent/AGENTS.md]\n"

func TestSpecNoPresetKeepsTodaysDefaults(t *testing.T) {
	s, err := ParseSpec([]byte(w4Good))
	if err != nil {
		t.Fatal(err)
	}
	if s.Commit.Alpha != 0.05 || s.Improver.Candidates != 1 || s.Improver.Keep != 1 || !s.Sealed.Curve || s.Budget.TotalUSD != 0 || s.Claim.Extension {
		t.Fatalf("no preset defaults: %+v", s)
	}
	if s.Claim.Alpha != 0.05 || s.Claim.ExtensionAlpha != 0.02 || s.Claim.TargetGain != 0.05 || s.Retries.MaxAttempts != 32 || s.Replicate.Loops != 1 {
		t.Fatalf("claim and retries defaults: %+v %+v %+v", s.Claim, s.Retries, s.Replicate)
	}
	if s.Improver.Task != DefaultImproverTask || DefaultImproverTask != "heimdall-improver@1.1.0:reflect-edit" {
		t.Fatalf("improver task %q", s.Improver.Task)
	}
}

func TestSpecImproverAliasExpanded(t *testing.T) {
	s, err := ParseSpec([]byte(w4Good + "improver: {task: \"improver@1.1.0:reflect-edit\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Improver.Task != DefaultImproverTask {
		t.Fatalf("alias kept: %q", s.Improver.Task)
	}
	s, err = ParseSpec([]byte(w4Good + "improver: {task: \"mine@2.0.0:rewrite\"}\n"))
	if err != nil || s.Improver.Task != "mine@2.0.0:rewrite" {
		t.Fatalf("custom task changed: %v %v", s, err)
	}
}

func TestSpecPresetsFill(t *testing.T) {
	cases := []struct {
		preset                      string
		rounds, k, keep, ts, tc, tz int
		merge, curve, ext           bool
		restart                     []int
		capUSD, limit               float64
	}{
		{"small", 3, 2, 2, 1, 2, 2, false, false, true, nil, 0.30, 15},
		{"medium", 5, 4, 4, 1, 3, 2, true, true, true, []int{3, 5}, 0.60, 60},
		{"large", 8, 8, 8, 2, 3, 3, true, true, false, []int{3, 6}, 1.50, 400},
	}
	for _, c := range cases {
		t.Run(c.preset, func(t *testing.T) {
			s, err := ParseSpec([]byte(w4Good + "preset: " + c.preset + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if s.Rounds != c.rounds || s.Commit.AuditLooks != c.rounds || s.Improver.Candidates != c.k || s.Improver.Keep != c.keep {
				t.Fatalf("counts %+v", s)
			}
			if s.Screen.Trials != c.ts || s.Commit.Trials != c.tc || s.Sealed.Trials != c.tz {
				t.Fatalf("trials %d %d %d", s.Screen.Trials, s.Commit.Trials, s.Sealed.Trials)
			}
			if s.Improver.Merge != c.merge || s.Sealed.Curve != c.curve || s.Claim.Extension != c.ext || !slices.Equal(s.Improver.RestartRounds, c.restart) {
				t.Fatalf("switches %+v", s)
			}
			if s.Improver.BudgetUSD != c.capUSD || s.Budget.TotalUSD != c.limit || s.Commit.Alpha != 0.10 {
				t.Fatalf("money %v %v alpha %v", s.Improver.BudgetUSD, s.Budget.TotalUSD, s.Commit.Alpha)
			}
			if !slices.Equal(s.Controls, []string{ControlNullImprover, ControlRetries}) {
				t.Fatalf("controls %v", s.Controls)
			}
		})
	}
	// Explicit fields win over the preset.
	s, err := ParseSpec([]byte(w4Good + "preset: medium\nrounds: 2\nimprover: {candidates: 3, budget_usd: 0.1}\nbudget: {total_usd: 9}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Rounds != 2 || s.Improver.Candidates != 3 || s.Improver.BudgetUSD != 0.1 || s.Budget.TotalUSD != 9 || s.Improver.Keep != 4 {
		t.Fatalf("explicit lost: %+v", s)
	}
}

func TestSpecInstructionsEditable(t *testing.T) {
	doc := "name: loop\nagent: agents/x.yaml\nsuite: s@1\neditable:\n  instructions: true\n"
	s, err := ParseSpec([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Editable.Allows(InstructionsKey) || InstructionsKey != "@instructions" {
		t.Fatal("instructions not allowed")
	}
	if (Editable{Files: []string{InstructionsKey}}).Allows(InstructionsKey) {
		t.Fatal("instructions allowed without the switch")
	}
}

func TestSpecValidateW4Rules(t *testing.T) {
	cases := map[string]string{
		"candidates":       "improver: {candidates: 9}\n",
		"keep":             "improver: {keep: 9}\n",
		"restart rounds":   "rounds: 3\nimprover: {restart_rounds: [4]}\n",
		"preset no limit":  "preset: custom\n",
		"unknown preset":   "preset: huge\nbudget: {total_usd: 5}\n",
		"claim alpha":      "claim: {alpha: 0.6}\n",
		"extension alpha":  "claim: {alpha: 0.05, extension_alpha: 0.05}\n",
		"target gain":      "claim: {target_gain: 1.5}\n",
		"retries":          "retries: {max_attempts: 33}\n",
		"loops":            "replicate: {loops: 6}\n",
		"transfer both":    "replicate: {loops: 2, transfer: {suite: \"a@1\", model: m, trials: 2}}\n",
		"transfer neither": "replicate: {loops: 2, transfer: {trials: 2}}\n",
		"improver agent":   "improver: {agent: other}\n",
		"model alone":      "improver: {model: gpt}\n",
		"tool surfaces":    "surfaces: {tool_descriptions: true}\n",
		"control":          "controls: [retries_v1]\n",
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSpec([]byte(w4Good + extra)); err == nil {
				t.Error("accepted")
			}
		})
	}
	_, err := ParseSpec([]byte(w4Good + "surfaces: {tool_descriptions: true}\n"))
	if err == nil || !strings.Contains(err.Error(), "Editing tool descriptions is not switched on on this server.") {
		t.Fatalf("tool surfaces text: %v", err)
	}
	t.Setenv(EnvToolDescriptions, "1")
	if _, err := ParseSpec([]byte(w4Good + "surfaces: {tool_descriptions: true}\n")); err != nil {
		t.Fatalf("switched on: %v", err)
	}
	for _, ok := range []string{"improver: {agent: reflector}\n", "improver: {agent: self, model: m, provider: p}\n", "controls: [retries_v0, null_improver]\n",
		"replicate: {loops: 3, repartition: true, transfer: {model: m, trials: 2}}\n", "commit: {min_pairs: 2, max_pairs: 9}\n"} {
		if _, err := ParseSpec([]byte(w4Good + ok)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}
