package taskgates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// suiteOnDisk writes an answer suite with one task per instruction text.
func suiteOnDisk(t *testing.T, id string, texts map[string]string) *schemas.Suite {
	t.Helper()
	root := filepath.Join(t.TempDir(), id)
	s := &schemas.Suite{ID: id, Root: root, Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "suite.yaml"), []byte("id: "+id+"\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for tid, text := range texts {
		dir := filepath.Join(root, "tasks", tid)
		if err := os.MkdirAll(filepath.Join(dir, "verifier"), 0o755); err != nil {
			t.Fatal(err)
		}
		must(t, os.WriteFile(filepath.Join(dir, "instruction.md"), []byte(text+"\n"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "verifier", "answer.yaml"), []byte("match: exact\nexpected: [\"42\"]\n"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "task.yaml"), []byte("id: "+tid+"\nversion: 1.0.0\ninstruction: instruction.md\n"), 0o644))
		s.Tasks = append(s.Tasks, &schemas.Task{ID: tid, Version: "1.0.0", Instruction: "instruction.md", Dir: dir})
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const refundText = "A customer writes that order A-1001 arrived broken and asks for a refund. Look up the order, check the refund policy, issue the refund and reply with the amount refunded and the expected date."

func TestDedupExactAndNear(t *testing.T) {
	items := []Item{
		{Suite: "s", Task: "a", Instruction: refundText},
		{Suite: "s", Task: "b", Instruction: strings.ToUpper(refundText) + "  "},                              // exact after normalising
		{Suite: "s", Task: "c", Instruction: strings.Replace(refundText, "expected date", "expected day", 1)}, // near
		{Suite: "s", Task: "d", Instruction: "Rename the config flag verbose to debug in every module and update the docs."},
		{Suite: "s", Task: "e", Instruction: ""},
	}
	got := Dedup(items, nil, 0)
	kinds := map[string]string{}
	for _, p := range got {
		kinds[p.A+"|"+p.B] = p.Kind
		if p.Kind == DupNear && (p.Jaccard < DefaultNearThreshold || p.Jaccard >= 1) {
			t.Fatalf("near pair %v: jaccard out of range", p)
		}
	}
	if kinds["s/a|s/b"] != DupExact || kinds["s/a|s/c"] != DupNear || kinds["s/b|s/c"] != DupNear {
		t.Fatalf("pairs %v", got)
	}
	for k := range kinds {
		if strings.Contains(k, "s/d") || strings.Contains(k, "s/e") {
			t.Fatalf("unrelated or empty task reported: %v", got)
		}
	}
	// scope: only pairs touching the new task c
	scoped := Dedup(items, map[string]bool{"s/c": true}, 0)
	for _, p := range scoped {
		if p.A != "s/c" && p.B != "s/c" {
			t.Fatalf("scoped dedup reported %v", p)
		}
	}
	if len(scoped) != 2 {
		t.Fatalf("scoped pairs %v", scoped)
	}
}

func TestDedupIgnoresCanaryLine(t *testing.T) {
	guid := utils.NewCanary()
	a := Item{Suite: "s", Task: "a", Instruction: StripCanary(refundText + "\n\n" + utils.CanaryLine("instruction.md", guid) + "\n")}
	b := Item{Suite: "s", Task: "b", Instruction: refundText}
	if got := Dedup([]Item{a, b}, nil, 0); len(got) != 1 || got[0].Kind != DupExact {
		t.Fatalf("stamped copy not an exact duplicate: %v", got)
	}
}

func TestContamination(t *testing.T) {
	lib := NewIndex([]Item{
		{Suite: "lib", Task: "refund", Instruction: refundText},
		{Suite: "lib", Task: "short", Instruction: "What is the capital of France?"},
	})
	items := []Item{
		{Suite: "own", Task: "copied", Instruction: "Please help. " + refundText},
		{Suite: "own", Task: "short", Instruction: "what is the CAPITAL of france"},
		{Suite: "own", Task: "fresh", Instruction: "Draft a weekly status note for the platform team covering the three incidents and their fixes."},
	}
	hits := lib.Check(items, func(it Item) string {
		if it.Task == "copied" {
			return schemas.SplitHeldOut
		}
		return schemas.SplitPublic
	})
	if len(hits) != 2 {
		t.Fatalf("hits %v", hits)
	}
	if hits[0].Task != "own/copied" || hits[0].Severity != SeverityError || hits[0].Matches[0] != "lib/refund" || hits[0].Overlap < 0.8 {
		t.Fatalf("copied hit %+v", hits[0])
	}
	if hits[1].Task != "own/short" || hits[1].Severity != SeverityWarning {
		t.Fatalf("short hit %+v", hits[1])
	}
	// a library task never matches itself
	if self := lib.Check([]Item{{Suite: "lib", Task: "refund", Instruction: refundText}}, nil); len(self) != 0 {
		t.Fatalf("self match %v", self)
	}
}

func TestCheckReport(t *testing.T) {
	s := suiteOnDisk(t, "own", map[string]string{"t1": refundText, "t2": refundText, "t3": "Write a haiku about Go modules and the go.sum file, then explain each line."})
	s.TaskByID("t1").Split = schemas.SplitHeldOut
	ix := NewIndex([]Item{{Suite: "lib", Task: "refund", Instruction: refundText}})
	r := Check(s, ix, 0)
	if len(r.Duplicates) != 1 || len(r.Contamination) != 2 || r.LibraryTasks != 1 {
		t.Fatalf("report %+v", r)
	}
	if r.Errors != 1 || r.Warnings != 2 || !strings.Contains(r.Summary, "already public") {
		t.Fatalf("report counts %+v", r)
	}
}

func TestPlanSplitSeededAndCanary(t *testing.T) {
	texts := map[string]string{}
	for i := 0; i < 8; i++ {
		texts["t"+string(rune('a'+i))] = "Own task number " + string(rune('a'+i)) + " about the billing service and its retry rules."
	}
	s := suiteOnDisk(t, "own", texts)
	p := SplitPlan{HeldOutPct: 25, Seed: 7}
	res, edits, err := PlanSplit(s, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.HeldOut != 2 || res.Public != 6 || !utils.ValidCanary(res.Canary) {
		t.Fatalf("result %+v", res)
	}
	res2, _, _ := PlanSplit(s, p, nil)
	var a, b []string
	for i := range res.Tasks {
		if res.Tasks[i].Split == schemas.SplitHeldOut {
			a = append(a, res.Tasks[i].Task)
		}
		if res2.Tasks[i].Split == schemas.SplitHeldOut {
			b = append(b, res2.Tasks[i].Task)
		}
	}
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("same seed, different split: %v vs %v", a, b)
	}
	// suite.yaml canary + 2 task.yaml + 2 instructions
	if len(edits) != 5 {
		t.Fatalf("edits %d", len(edits))
	}
	must(t, WriteEdits(s.Root, edits))
	raw, _ := os.ReadFile(filepath.Join(s.Root, "suite.yaml"))
	if utils.SuiteYAMLCanary(raw) != res.Canary {
		t.Fatalf("suite.yaml canary not set: %s", raw)
	}
	for _, id := range a {
		ty, _ := os.ReadFile(filepath.Join(s.Root, "tasks", id, "task.yaml"))
		in, _ := os.ReadFile(filepath.Join(s.Root, "tasks", id, "instruction.md"))
		if !strings.Contains(string(ty), "split: held_out") || !strings.Contains(string(in), res.Canary) {
			t.Fatalf("task %s not held out and stamped:\n%s\n%s", id, ty, in)
		}
	}
	for _, c := range res.Tasks {
		if c.Split == schemas.SplitPublic {
			in, _ := os.ReadFile(filepath.Join(s.Root, "tasks", c.Task, "instruction.md"))
			if strings.Contains(string(in), res.Canary) {
				t.Fatalf("public task %s stamped", c.Task)
			}
		}
	}
}

func TestPlanSplitStratified(t *testing.T) {
	texts := map[string]string{}
	for i := 0; i < 8; i++ {
		texts["t"+string(rune('a'+i))] = "Task " + string(rune('a'+i)) + " with its own words for the split test."
	}
	s := suiteOnDisk(t, "own", texts)
	for i, tk := range s.Tasks {
		if i%2 == 0 {
			tk.Difficulty = "easy"
		} else {
			tk.Difficulty = "hard"
		}
	}
	res, _, err := PlanSplit(s, SplitPlan{HeldOutPct: 50, Seed: 1, StratifyBy: "difficulty"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	per := map[string]int{}
	for _, c := range res.Tasks {
		if c.Split == schemas.SplitHeldOut {
			per[c.Stratum]++
		}
	}
	if per["easy"] != 2 || per["hard"] != 2 {
		t.Fatalf("per stratum %v", per)
	}
}

func TestPlanSplitRefusesPublicData(t *testing.T) {
	s := suiteOnDisk(t, "own", map[string]string{"t1": refundText, "t2": "Another own task text that nobody has seen before today."})
	s.TaskByID("t1").Provenance = &schemas.TaskProvenance{ImportedFrom: "tau-bench@1.0"}
	s.TaskByID("t2").Provenance = &schemas.TaskProvenance{ImportedFrom: "tau-bench@1.0"}
	res, edits, err := PlanSplit(s, SplitPlan{HeldOutPct: 50, Seed: 3}, nil)
	if !errors.Is(err, ErrPublicHeldOut) || edits != nil || len(res.Refused) != 1 {
		t.Fatalf("err %v edits %v res %+v", err, edits, res)
	}
	if _, edits, err = PlanSplit(s, SplitPlan{HeldOutPct: 50, Seed: 3, Confirm: true}, nil); err != nil || len(edits) == 0 {
		t.Fatalf("confirmed split: %v", err)
	}
	// overlap with the library is public data too
	s2 := suiteOnDisk(t, "own2", map[string]string{"t1": refundText})
	ix := NewIndex([]Item{{Suite: "lib", Task: "refund", Instruction: refundText}})
	if _, _, err := PlanSplit(s2, SplitPlan{HeldOutPct: 90, Seed: 3}, ix); !errors.Is(err, ErrPublicHeldOut) {
		t.Fatalf("library overlap: err %v", err)
	}
}

func TestCheckSplitPlan(t *testing.T) {
	for _, p := range []SplitPlan{{HeldOutPct: 95}, {HeldOutPct: -1}, {}, {HeldOutPct: 10, StratifyBy: "colour"}, {TimeAfter: "yesterday"}} {
		if CheckSplitPlan(p) == nil {
			t.Fatalf("plan %+v accepted", p)
		}
	}
}
