package openaievals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/suite"
)

func runImport(t *testing.T, extra map[string]string) (*importers.Result, string) {
	t.Helper()
	root := t.TempDir()
	res, err := importer{}.Import(context.Background(), importers.Options{
		BenchRoot: root, From: "testdata", SuiteID: "evals-qa", Extra: extra,
	})
	if err != nil {
		t.Fatalf("import: %v (%+v)", err, res)
	}
	return res, filepath.Join(root, "benchmarks", "evals-qa")
}

func TestImportMatchAndIncludes(t *testing.T) {
	res, dir := runImport(t, nil)
	if len(res.Tasks) != 6 {
		t.Fatalf("tasks %v skipped %+v", res.Tasks, res.Skipped)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.ID] = s.Reason
	}
	if !strings.Contains(reasons["tone"], "judge suite") || !strings.Contains(reasons["custom"], "no Heimdall mapping") {
		t.Fatalf("skips %+v", res.Skipped)
	}
	ans, err := os.ReadFile(filepath.Join(dir, "tasks", "capitals-1", "verifier", "answer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ans), "match: regex") || !strings.Contains(string(ans), `^\s*Paris`) {
		t.Fatalf("Match answer.yaml:\n%s", ans)
	}
	instr, _ := os.ReadFile(filepath.Join(dir, "tasks", "capitals-1", "instruction.md"))
	if !strings.Contains(string(instr), "system: Answer with the city name only.") || !strings.Contains(string(instr), "user: What is the capital of France?") {
		t.Fatalf("instruction:\n%s", instr)
	}
	inc, _ := os.ReadFile(filepath.Join(dir, "tasks", "capitals-includes-2", "verifier", "answer.yaml"))
	if !strings.Contains(string(inc), "(?i)Tokyo") {
		t.Fatalf("Includes answer.yaml:\n%s", inc)
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "tasks", "capitals-3", "solution", "answer.txt"))
	if strings.TrimSpace(string(ref)) != "Lima" {
		t.Fatalf("reference %q", ref)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if !strings.Contains(string(raw), "capitals-3") || !strings.Contains(string(raw), "grader: answer_match") || strings.Contains(string(raw), "hotel-cap") {
		t.Fatalf("suite.yaml:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal("no .gitignore")
	}
}

func TestImportModelGradedIntoJudgeSuite(t *testing.T) {
	res, dir := runImport(t, map[string]string{SetGrader: "judge", SetJudgeProvider: "openai", SetJudgeModel: "gpt-x", SetEvals: "tone"})
	if len(res.Tasks) != 1 || res.Tasks[0] != "tone-1" {
		t.Fatalf("tasks %v skipped %+v", res.Tasks, res.Skipped)
	}
	ans, _ := os.ReadFile(filepath.Join(dir, "tasks", "tone-1", "verifier", "answer.yaml"))
	s := string(ans)
	if !strings.Contains(s, "polite and thanks the sender") || !strings.Contains(s, "{completion}") || !strings.Contains(s, "choice would be: Y") {
		t.Fatalf("rubric:\n%s", s)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if !strings.Contains(string(raw), "grader: judge") || !strings.Contains(string(raw), "model: gpt-x") {
		t.Fatalf("suite.yaml:\n%s", raw)
	}
}

func TestImportRefusesBadSettings(t *testing.T) {
	root := t.TempDir()
	for _, extra := range []map[string]string{{"bogus": "1"}, {SetGrader: "vibes"}, {SetGrader: "judge"}} {
		if _, err := (importer{}).Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "x", Extra: extra}); err == nil {
			t.Errorf("%v accepted", extra)
		}
	}
	if _, err := (importer{}).Import(context.Background(), importers.Options{BenchRoot: root, From: t.TempDir(), SuiteID: "x"}); err == nil {
		t.Error("folder without a registry accepted")
	}
}

func TestLimitAndLFSPointer(t *testing.T) {
	res, _ := runImport(t, map[string]string{SetEvals: "capitals"})
	if len(res.Tasks) != 3 {
		t.Fatalf("tasks %v", res.Tasks)
	}
	reg := t.TempDir()
	for _, d := range []string{"evals", "data/x"} {
		_ = os.MkdirAll(filepath.Join(reg, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(reg, "evals", "x.yaml"), []byte("x:\n  id: x.v0\nx.v0:\n  class: evals.elsuite.basic.match:Match\n  args:\n    samples_jsonl: x/samples.jsonl\n"), 0o644)
	_ = os.WriteFile(filepath.Join(reg, "data", "x", "samples.jsonl"), []byte("version https://git-lfs.github.com/spec/v1\noid sha256:abc\n"), 0o644)
	res2, err := (importer{}).Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: reg, SuiteID: "x"})
	if err == nil || len(res2.Skipped) != 1 || !strings.Contains(res2.Skipped[0].Reason, "git lfs pull") {
		t.Fatalf("lfs pointer: %v %+v", err, res2)
	}
}
