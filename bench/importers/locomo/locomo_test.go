package locomo

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

var fixedNow = func() time.Time { return time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC) }

func runImport(t *testing.T, extra map[string]string, ids []string, limit int) (string, *importers.Result) {
	t.Helper()
	root := t.TempDir()
	im := &importer{now: fixedNow}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "loc", IDs: ids, Limit: limit, Extra: extra})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, res
}

func readFile(t *testing.T, path ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readAnswer(t *testing.T, root, task string) schemas.AnswerSpec {
	t.Helper()
	var a schemas.AnswerSpec
	dec := yaml.NewDecoder(strings.NewReader(readFile(t, root, "benchmarks", "loc", "tasks", task, "verifier", "answer.yaml")))
	dec.KnownFields(true)
	if err := dec.Decode(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

func skipReasons(res *importers.Result) map[string]string {
	out := map[string]string{}
	for _, s := range res.Skipped {
		out[s.ID] = s.Reason
	}
	return out
}

func TestRegistered(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("locomo importer not registered")
	}
	if info := im.Info(); !strings.Contains(info.License, "CC-BY-NC-4.0") || !strings.Contains(info.License, "non-commercial") {
		t.Fatalf("license %q", info.License)
	}
}

func TestImportJudged(t *testing.T) {
	root, res := runImport(t, nil, nil, 0)
	want := []string{"conv-fx1-q001", "conv-fx1-q002", "conv-fx1-q003", "conv-fx1-q005", "conv-fx1-q006"}
	if !reflect.DeepEqual(res.Tasks, want) {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	reasons := skipReasons(res)
	if !strings.Contains(reasons["conv-fx1-q004"], "both answer and adversarial_answer") || !strings.Contains(reasons["conv-fx1-q007"], "unknown category 9") ||
		!strings.Contains(reasons["conv-fx2-q001"], "session_1_date_time") {
		t.Fatalf("skip reasons %v", reasons)
	}
	if !strings.Contains(res.Notes[0], "CC BY-NC 4.0") || !strings.Contains(res.Notes[1], "token F1") {
		t.Fatalf("notes %v", res.Notes)
	}
	suiteYAML := readFile(t, root, "benchmarks", "loc", "suite.yaml")
	for _, s := range []string{"grader: judge", "grading: judged", "prompt_version: qa-v1", "license: CC-BY-NC-4.0", "track: memory"} {
		if !strings.Contains(suiteYAML, s) {
			t.Fatalf("suite.yaml lacks %q:\n%s", s, suiteYAML)
		}
	}
	single := readAnswer(t, root, "conv-fx1-q001")
	if single.Category != "single-hop" || !reflect.DeepEqual(single.Expected, []string{"Kiwi"}) || single.Abstain || len(single.WrongOnes) != 1 {
		t.Fatalf("single-hop spec %+v", single)
	}
	if year := readAnswer(t, root, "conv-fx1-q002"); year.Category != "temporal" || !reflect.DeepEqual(year.WrongOnes[1:], []string{"2027"}) {
		t.Fatalf("temporal spec %+v", year)
	}
	adv := readAnswer(t, root, "conv-fx1-q003")
	if !adv.Abstain || adv.Category != "adversarial" || !reflect.DeepEqual(adv.WrongOnes, []string{"a medal"}) || !strings.Contains(adv.Expected[0], "Ana and Ben") {
		t.Fatalf("adversarial spec %+v", adv)
	}
	if ref := readFile(t, root, "benchmarks", "loc", "tasks", "conv-fx1-q003", "solution", "answer.txt"); !strings.Contains(ref, "not mentioned") {
		t.Fatalf("adversarial reference %q", ref)
	}
}

func TestImportSessions(t *testing.T) {
	root, _ := runImport(t, nil, []string{"conv-fx1-q002"}, 0)
	task := filepath.Join(root, "benchmarks", "loc", "tasks", "conv-fx1-q002")
	// session_11 is empty and dropped; sessions 1, 2, 10 in numeric order, one per part.
	s1 := readFile(t, task, "sessions", "s1-files", "history", "conversation-001.md")
	s2 := readFile(t, task, "sessions", "s2-files", "history", "conversation-002.md")
	s3 := readFile(t, task, "sessions", "s3-files", "history", "conversation-003.md")
	if !strings.Contains(s1, "3 March, 2022") || !strings.Contains(s2, "[shares a photo: a man holding a medal]") || !strings.Contains(s3, "1 June, 2023") {
		t.Fatalf("sessions out of order or captions missing:\n%s\n%s\n%s", s1, s2, s3)
	}
	if strings.Contains(s2, "example.invalid") || strings.Contains(s1, "D1:1") {
		t.Fatalf("image url or dialog id leaked:\n%s\n%s", s1, s2)
	}
	q := readFile(t, task, "sessions", "question.md")
	if !strings.Contains(q, "When did Ana adopt her parrot?") || !strings.Contains(q, "dates of the conversations") {
		t.Fatalf("question:\n%s", q)
	}
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "loc")
	if err != nil {
		t.Fatalf("load imported suite: %v", err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error %s: %s", p.Path, p.Message)
		}
	}
}

func TestImportMatch(t *testing.T) {
	root, res := runImport(t, map[string]string{"grading": "match"}, []string{"conv-fx1"}, 0)
	if !reflect.DeepEqual(res.Tasks, []string{"conv-fx1-q002", "conv-fx1-q003", "conv-fx1-q005"}) {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	if !strings.Contains(skipReasons(res)["conv-fx1-q001"], "free-text") {
		t.Fatalf("skips %v", res.Skipped)
	}
	if a := readAnswer(t, root, "conv-fx1-q005"); a.Match != "numeric" || !reflect.DeepEqual(a.Expected, []string{"3"}) {
		t.Fatalf("numeric spec %+v", a)
	}
	if a := readAnswer(t, root, "conv-fx1-q003"); !a.Abstain || len(a.Expected) != 0 {
		t.Fatalf("adversarial spec %+v", a)
	}
}

func TestImportLimitAndIDs(t *testing.T) {
	_, res := runImport(t, nil, nil, 2)
	if !reflect.DeepEqual(res.Tasks, []string{"conv-fx1-q001", "conv-fx1-q002"}) {
		t.Fatalf("limit: tasks %v", res.Tasks)
	}
	_, res = runImport(t, nil, []string{"conv-fx1-q006", "conv-zz"}, 0)
	if !reflect.DeepEqual(res.Tasks, []string{"conv-fx1-q006"}) || !strings.Contains(skipReasons(res)["conv-zz"], "not in locomo10.json") {
		t.Fatalf("ids: tasks %v skipped %v", res.Tasks, res.Skipped)
	}
}

func TestImportRefusesUnknownSetting(t *testing.T) {
	im := &importer{now: fixedNow}
	_, err := im.Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: "testdata", SuiteID: "x", Extra: map[string]string{"variant": "s"}})
	if err == nil || !strings.Contains(err.Error(), "unknown setting") {
		t.Fatalf("err = %v", err)
	}
	_, err = im.Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: t.TempDir(), SuiteID: "x"})
	if err == nil || !strings.Contains(err.Error(), "no locomo10.json") {
		t.Fatalf("err = %v", err)
	}
}

// TestImportRealData imports questions from a local clone of the upstream
// repository and lints the suite. It runs only when BENCH_LOCOMO_FROM names
// the clone or the data file (BENCH_LOCOMO_OUT keeps the bench root).
func TestImportRealData(t *testing.T) {
	from := os.Getenv("BENCH_LOCOMO_FROM")
	if from == "" {
		t.Skip("BENCH_LOCOMO_FROM not set")
	}
	root := os.Getenv("BENCH_LOCOMO_OUT")
	if root == "" {
		root = t.TempDir()
	}
	for _, grading := range []string{"judged", "match"} {
		t.Run(grading, func(t *testing.T) {
			id := "locomo-" + grading
			im := &importer{now: time.Now}
			res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: id, IDs: []string{"conv-26"}, Limit: 200, Force: true,
				Extra: map[string]string{"grading": grading}})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %d tasks, %d skipped", id, len(res.Tasks), len(res.Skipped))
			for _, n := range res.Notes {
				t.Logf("note: %s", n)
			}
			if len(res.Tasks) == 0 {
				t.Fatal("no task imported")
			}
			s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), id)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			problems := suite.Lint(s)
			for _, p := range problems {
				if p.Severity == suite.SeverityError {
					t.Errorf("lint error %s: %s", p.Path, p.Message)
				}
			}
			t.Logf("lint: %d tasks, %d problems", len(s.Tasks), len(problems))
		})
	}
}
