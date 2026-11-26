package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
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
	doc := []byte("id: a\nmodel: inc-model\ngateway:\n  provider: anthropic\nbudgets:\n  wall_s: 900\nfiles:\n  /home/agent/AGENTS.md: hi\n")
	req := improve.ImproveRequest{Spec: &improve.Spec{Editable: improve.Editable{Files: []string{"/home/agent/AGENTS.md"}}, Improver: improve.ImproverSpec{Agent: "self"}}, Round: 2,
		Incumbent: improve.Version{Doc: doc, Bundle: improve.Bundle{"/home/agent/AGENTS.md": "hi"}},
		Evidence:  []improve.EvidenceItem{{Task: "t1", Outcome: "failed"}}, BudgetUSD: 1.5, WallS: 600}
	for _, null := range []bool{false, true} {
		req.Null = null
		out, err := improverDoc(req, doc, improve.PlaybookObject{})
		if err != nil {
			t.Fatal(err)
		}
		var tree struct {
			Budgets      map[string]any    `yaml:"budgets"`
			Files        map[string]string `yaml:"files"`
			FilesRaw     map[string]string `yaml:"files_raw"`
			Instructions string            `yaml:"instructions"`
		}
		if err := yaml.Unmarshal(out, &tree); err != nil {
			t.Fatal(err)
		}
		if tree.Budgets["usd"] != 1.5 || tree.Budgets["wall_s"] != 600 || tree.Files["/home/agent/AGENTS.md"] != "hi" {
			t.Errorf("doc %s", out)
		}
		ev := tree.FilesRaw[improverEvidencePath]
		if strings.Contains(ev, `"t1"`) == null {
			t.Errorf("null %v evidence %s", null, ev)
		}
		if null != strings.Contains(tree.Instructions, improverNullLine) {
			t.Errorf("null %v instructions %q", null, tree.Instructions)
		}
		var b struct {
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal([]byte(tree.FilesRaw[improverBundlePath]), &b); err != nil || b.Files["/home/agent/AGENTS.md"] != "hi" {
			t.Errorf("bundle %v %v", b, err)
		}
		if tree.FilesRaw[improverLimitsPath] == "" {
			t.Error("no limits.json")
		}
	}
}

// The reflector runs on the incumbent's model and provider (or the
// teacher's), and the evidence document's files reach it raw, "{{" and all.
func TestImproverDocReflector(t *testing.T) {
	inc := []byte("id: a\nmodel: inc-model\ngateway:\n  provider: anthropic\n")
	reflector := []byte("id: heimdall-reflector\nmodel: gpt-x\ngateway:\n  provider: openai\nfiles:\n  /home/agent/x: y\n")
	req := improve.ImproveRequest{Spec: &improve.Spec{}, Round: 1, Incumbent: improve.Version{Doc: inc, Bundle: improve.Bundle{}}, Kind: "restart",
		EvidenceDoc: &improve.EvidenceDoc{EvidenceJSON: []byte(`{"split":"dev","items":[]}`), Files: map[string][]byte{"runs/t1/1/B/digest.md": []byte("saw {{model}}")}}}
	out, err := improverDoc(req, reflector, improve.PlaybookObject{})
	if err != nil {
		t.Fatal(err)
	}
	var tree schemas.AgentConfig
	if err := yaml.Unmarshal(out, &tree); err != nil {
		t.Fatal(err)
	}
	if tree.Model != "inc-model" || tree.Gateway.Provider != "anthropic" || tree.ID != "heimdall-reflector" {
		t.Errorf("model %q provider %q id %q", tree.Model, tree.Gateway.Provider, tree.ID)
	}
	if tree.FilesRaw[improverDir+"/runs/t1/1/B/digest.md"] != "saw {{model}}" || tree.FilesRaw[improverEvidencePath] != `{"split":"dev","items":[]}` {
		t.Errorf("files_raw %v", tree.FilesRaw)
	}
	if !strings.Contains(tree.Instructions, improverRestartLine) {
		t.Errorf("instructions %q", tree.Instructions)
	}
	req.Model, req.Provider = "teacher", "openai"
	out, _ = improverDoc(req, reflector, improve.PlaybookObject{})
	if err := yaml.Unmarshal(out, &tree); err != nil || tree.Model != "teacher" || tree.Gateway.Provider != "openai" {
		t.Errorf("teacher %q %q %v", tree.Model, tree.Gateway.Provider, err)
	}
	req.EvidenceDoc.Files = map[string][]byte{"../escape": nil}
	if _, err := improverDoc(req, reflector, improve.PlaybookObject{}); err == nil {
		t.Error("escaping evidence path accepted")
	}
}

func TestReadImproverOutput(t *testing.T) {
	dir := t.TempDir()
	inc := improve.Bundle{"/a": "x"}
	res := &improve.ImproveResult{}
	if err := readImproverOutput(dir, inc, res); err == nil {
		t.Fatal("no artifact accepted")
	}
	p := filepath.Join(dir, "bundle", improverBundleFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readImproverOutput(dir, inc, res); err == nil {
		t.Fatal("bad json accepted")
	}
	if err := os.WriteFile(p, []byte(`{"files":{"/a":"y"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readImproverOutput(dir, inc, res); err == nil || err.Error() != "the improver gave no reason for /a" {
		t.Fatalf("no reason: %v", err)
	}
	if err := os.WriteFile(p, []byte(`{"files":{"/a":"y"},"reasons":{"/a":"tighter"},"summary":"one change"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readImproverOutput(dir, inc, res); err != nil || res.Bundle["/a"] != "y" || res.Reasons["/a"] != "tighter" || res.Summary != "one change" {
		t.Fatalf("%+v %v", res, err)
	}
	ep := filepath.Join(dir, "edits", improverEditsFile)
	if err := os.MkdirAll(filepath.Dir(ep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep, []byte(`{"rationale":"r","edits":[{"op":"add_item","file":"@instructions","text":"t","reason":"why"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res = &improve.ImproveResult{}
	if err := readImproverOutput(dir, inc, res); err != nil || res.Edits == nil || len(res.Edits.Edits) != 1 || res.Bundle["/a"] != "x" {
		t.Fatalf("edits %+v %v", res, err)
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

// Freeze keeps the whole applied bundle (a skill added under an editable
// folder survives), stores the bundle object, and refuses a bundle whose
// hash differs from the one the runner applied.
func TestImproveFreezeKeepsWholeBundle(t *testing.T) {
	root := t.TempDir()
	st, err := store.NewFSStore(filepath.Join(root, "runs"), utils.NewStdLogger(io.Discard, false))
	if err != nil {
		t.Fatal(err)
	}
	x := improveExecutor{a: &app{cfg: &lib.Config{Root: root}, store: st}}
	ed := improve.Editable{Files: []string{"/home/agent/AGENTS.md"}, Dirs: []string{"/home/agent/skills/"}, Instructions: true}
	parentDoc := []byte("id: fz\nadapter: command\nimage: img\nargv: [run]\nmodel: m\nfiles:\n  /home/agent/AGENTS.md: hi\n")
	parent, err := x.snapshot("fz", parentDoc, ed)
	if err != nil {
		t.Fatal(err)
	}
	b := improve.Bundle{"/home/agent/AGENTS.md": "hi", "/home/agent/skills/run/SKILL.md": "run {{model}} tests", improve.InstructionsKey: "Be brief."}
	doc, err := improve.ApplyBundle(parent.Doc, ed, b)
	if err != nil {
		t.Fatal(err)
	}
	nv, err := x.Freeze(context.Background(), parent, doc, improve.Lineage{ImprovementID: "imp_fz", Round: 1, BundleHash: b.Hash(), Editable: ed})
	if err != nil {
		t.Fatal(err)
	}
	if nv.Bundle["/home/agent/skills/run/SKILL.md"] != "run {{model}} tests" || nv.Bundle[improve.InstructionsKey] != "Be brief." || nv.BundleHash != b.Hash() {
		t.Errorf("frozen bundle %v hash %s", nv.Bundle, nv.BundleHash)
	}
	var obj improve.BundleObject
	objs := improve.Objects{Root: filepath.Join(st.Root(), improve.DefaultDir, "objects")}
	if nv.BundleObj == "" || objs.Get(nv.BundleObj, improve.ObjBundle, &obj) != nil || obj.Files["/home/agent/skills/run/SKILL.md"] == "" || !slices.Contains(obj.Raw, "/home/agent/skills/run/SKILL.md") {
		t.Errorf("bundle object %q %+v", nv.BundleObj, obj)
	}
	_, err = x.Freeze(context.Background(), parent, doc, improve.Lineage{ImprovementID: "imp_fz", Round: 1, BundleHash: "abc", Editable: ed})
	if err == nil || !strings.HasPrefix(err.Error(), "frozen bundle ") || !strings.HasSuffix(err.Error(), " does not match the applied bundle abc") {
		t.Errorf("mismatch: %v", err)
	}
}

// Scoring reads up to six arms (A to F), the run id and the visible stop.
func TestImproveRunScoreArmsAndStop(t *testing.T) {
	for _, c := range []struct {
		arm     string
		outcome schemas.Outcome
		idx     int
		stop    string
	}{
		{"A", schemas.OutcomePass, 0, ""},
		{"C", schemas.OutcomeTimeout, 2, "timeout"},
		{"F", schemas.OutcomeRefusal, 5, "refusal"},
		{"E", schemas.OutcomeCrash, 4, "crash"},
		{"B", schemas.OutcomeBudget, 1, "budget"},
		{"D", schemas.OutcomeFail, 3, ""},
		{"G", schemas.OutcomePass, -1, ""},
	} {
		sr := schemas.SealedRun{Slot: schemas.Slot{Arm: c.arm, Task: "t1", Trial: 2, Counted: true}, Run: schemas.Run{RunID: "r_" + c.arm, Outcome: c.outcome}}
		rs, ok := improveRunScore(sr, 6)
		if c.idx < 0 {
			if ok {
				t.Errorf("arm %s accepted", c.arm)
			}
			continue
		}
		if !ok || rs.Arm != c.idx || rs.Stop != c.stop || rs.RunID != "r_"+c.arm || rs.Task != "t1" || rs.Trial != 2 {
			t.Errorf("arm %s: %+v %v", c.arm, rs, ok)
		}
	}
	if _, ok := improveRunScore(schemas.SealedRun{Slot: schemas.Slot{Arm: "C", Counted: true}}, 2); ok {
		t.Error("arm beyond the request accepted")
	}
}

// A missing improver task is refused with the plain sentence before any
// experiment exists.
func TestCheckImproverMissingTask(t *testing.T) {
	root := t.TempDir()
	for p, body := range map[string]string{
		"deploy/bench.yaml": "runs_root: runs\nbenchmarks_root: benchmarks\nserver:\n  listen: 127.0.0.1:1\n  mode: live\n",
		"agents/inc.yaml":   "id: inc\nadapter: command\nimage: img\nargv: [run]\nmodel: m\n",
		"benchmarks/.keep":  "",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, err := newApp(context.Background(), filepath.Join(root, "deploy", "bench.yaml"), appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec := &improve.Spec{Agent: "agents/inc.yaml", Suite: "s@1", Improver: improve.ImproverSpec{Task: "nope@1.0.0:x"}}
	err = improveExecutor{a: a}.CheckImprover(context.Background(), spec)
	if err == nil || err.Error() != "The improver task nope@1.0.0:x does not exist." {
		t.Fatalf("err %v", err)
	}
	if exps, err := a.store.ListExperiments(context.Background()); err == nil && len(exps) != 0 {
		t.Errorf("experiments exist after the refusal: %d", len(exps))
	}
}
