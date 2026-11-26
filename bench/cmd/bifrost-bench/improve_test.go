package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

func TestImproverTask(t *testing.T) {
	cases := []struct {
		task, suite, wantSuite, wantTask string
		err                              bool
	}{
		{"", "s@1", "", "", true},
		{"rewrite", "s@1", "s@1", "rewrite", false},
		{"improver@2.0.0:rewrite", "s@1", "improver@2.0.0", "rewrite", false},
	}
	for _, c := range cases {
		s, task, err := improverTask(&improve.Spec{Suite: c.suite, Improver: improve.ImproverSpec{Task: c.task}})
		if (err != nil) != c.err || s != c.wantSuite || task != c.wantTask {
			t.Errorf("%q: %q %q %v", c.task, s, task, err)
		}
	}
}

func TestImproverDoc(t *testing.T) {
	doc := []byte("id: a\nbudgets:\n  wall_s: 900\nfiles:\n  /home/agent/AGENTS.md: hi\n")
	req := improve.ImproveRequest{Spec: &improve.Spec{Editable: improve.Editable{Files: []string{"/home/agent/AGENTS.md"}}}, Round: 2,
		Incumbent: improve.Version{Doc: doc, Bundle: improve.Bundle{"/home/agent/AGENTS.md": "hi"}},
		Evidence:  []improve.EvidenceItem{{Task: "t1", Outcome: "failed"}}, BudgetUSD: 1.5, WallS: 600}
	for _, null := range []bool{false, true} {
		req.Null = null
		out, err := improverDoc(req)
		if err != nil {
			t.Fatal(err)
		}
		var tree struct {
			Budgets map[string]any    `yaml:"budgets"`
			Files   map[string]string `yaml:"files"`
		}
		if err := yaml.Unmarshal(out, &tree); err != nil {
			t.Fatal(err)
		}
		if tree.Budgets["usd"] != 1.5 || tree.Budgets["wall_s"] != 600 || tree.Files["/home/agent/AGENTS.md"] != "hi" {
			t.Errorf("doc %s", out)
		}
		ev := tree.Files[improverEvidencePath]
		if strings.Contains(ev, `"t1"`) == null {
			t.Errorf("null %v evidence %s", null, ev)
		}
		var b struct {
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal([]byte(tree.Files[improverBundlePath]), &b); err != nil || b.Files["/home/agent/AGENTS.md"] != "hi" {
			t.Errorf("bundle %v %v", b, err)
		}
	}
}

func TestReadBundleArtifact(t *testing.T) {
	dir := t.TempDir()
	if _, err := readBundleArtifact(dir); err == nil {
		t.Fatal("no artifact accepted")
	}
	p := filepath.Join(dir, "bundle", improverBundleFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleArtifact(dir); err == nil {
		t.Fatal("bad json accepted")
	}
	if err := os.WriteFile(p, []byte(`{"files":{"/a":"x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := readBundleArtifact(dir)
	if err != nil || b["/a"] != "x" {
		t.Fatalf("%v %v", b, err)
	}
}

// The improver's spend cap is on its gateway key: the improver experiment
// uses the equal budget rule, so the run key's USD limit is budgets.usd.
func TestImproverExperimentCapsTheKey(t *testing.T) {
	req := improve.ImproveRequest{ImprovementID: "imp_x", Spec: &improve.Spec{Seed: 3}, Round: 2, BudgetUSD: 1.25}
	exp := improverExperiment(req, "heimdall-improver@1.1.0", "reflect-edit", ".heimdall/improve/imp_x/improver-r02.yaml")
	if exp.Fairness != schemas.FairnessEqualBudget {
		t.Errorf("fairness %q, want %q", exp.Fairness, schemas.FairnessEqualBudget)
	}
	if exp.Budget.TotalUSD != 1.25 || exp.Base != ".heimdall/improve/imp_x/improver-r02.yaml" {
		t.Errorf("budget %v base %q", exp.Budget.TotalUSD, exp.Base)
	}
	if arm, ok := exp.Arms["A"]; !ok || arm.Label != "improver" {
		t.Errorf("arms %v", exp.Arms)
	}
}

// An agent that sets its own gateway spending limit is refused before any
// experiment exists, since that limit would replace the improver's cap.
func TestCheckImproverRefusesOwnSpendLimit(t *testing.T) {
	root := t.TempDir()
	doc := "id: capped\nadapter: scripted\nmodel: m\ngateway:\n  routing:\n    limits:\n      budget_usd: 5\n"
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "capped.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	x := improveExecutor{a: &app{cfg: &lib.Config{Root: root}}}
	spec := &improve.Spec{Agent: "agents/capped.yaml", Suite: "s@1"}
	spec.Defaults()
	err := x.CheckImprover(context.Background(), spec)
	want := "This agent sets its own gateway spending limit; remove gateway.routing.limits.budget_usd so the improver's cap can be enforced."
	if err == nil || err.Error() != want {
		t.Fatalf("err %v, want %q", err, want)
	}
	if !agentSetsSpendLimit([]byte(doc)) || agentSetsSpendLimit([]byte("id: a\n")) {
		t.Error("agentSetsSpendLimit")
	}
}
