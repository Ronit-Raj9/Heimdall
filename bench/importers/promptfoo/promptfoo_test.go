package promptfoo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

func TestImportAnswerMatch(t *testing.T) {
	root := t.TempDir()
	res, err := importer{}.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/project", SuiteID: "pf"})
	if err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if strings.Join(res.Tasks, ",") != "test-1,test-2" {
		t.Fatalf("tasks %v skipped %+v", res.Tasks, res.Skipped)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.ID] = s.Reason
	}
	for id, want := range map[string]string{"test-3": "judge suite", "test-4": "javascript has no Heimdall check", "test-5": "no reference answer", "test-6": "question, which the test does not set"} {
		if !strings.Contains(reasons[id], want) {
			t.Errorf("%s: reason %q, want %q", id, reasons[id], want)
		}
	}
	dir := filepath.Join(root, "benchmarks", "pf", "tasks")
	ans, _ := os.ReadFile(filepath.Join(dir, "test-2", "verifier", "answer.yaml"))
	if !strings.Contains(string(ans), "(?i)bonjour") {
		t.Fatalf("answer.yaml:\n%s", ans)
	}
	instr, _ := os.ReadFile(filepath.Join(dir, "test-1", "instruction.md"))
	if !strings.Contains(string(instr), "Question: How many days do customers have to ask for a refund?") {
		t.Fatalf("instruction:\n%s", instr)
	}
}

func TestImportJudge(t *testing.T) {
	root := t.TempDir()
	res, err := importer{}.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/project/promptfooconfig.yaml", SuiteID: "pfj",
		Extra: map[string]string{SetGrader: "judge", SetJudgeProvider: "openai", SetJudgeModel: "gpt-x"}, IDs: []string{"test-3"}})
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	ans, _ := os.ReadFile(filepath.Join(root, "benchmarks", "pfj", "tasks", "test-3", "verifier", "answer.yaml"))
	if !strings.Contains(string(ans), "Polite, thanks the sender") {
		t.Fatalf("answer.yaml:\n%s", ans)
	}
	ref, _ := os.ReadFile(filepath.Join(root, "benchmarks", "pfj", "tasks", "test-3", "solution", "answer.txt"))
	if !strings.Contains(string(ref), "Thank you for the invite") {
		t.Fatalf("reference %q", ref)
	}
}

func TestRenderAndFileRefs(t *testing.T) {
	if _, err := render("{% if x %}a{% endif %}", nil); err == nil {
		t.Fatal("nunjucks tags accepted")
	}
	if got, err := render("Hi {{ name }}", map[string]any{"name": "Ada"}); err != nil || got != "Hi Ada" {
		t.Fatalf("%q %v", got, err)
	}
	if _, _, err := readFileRef("file://../../etc/passwd", t.TempDir()); err == nil {
		t.Fatal("file ref outside the folder accepted")
	}
}
