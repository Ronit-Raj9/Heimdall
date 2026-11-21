package longmemeval

import (
	"context"
	"encoding/json"
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

func runImport(t *testing.T, extra map[string]string, ids []string) (string, *importers.Result) {
	t.Helper()
	root := t.TempDir()
	im := &importer{now: fixedNow}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "lme", IDs: ids, Extra: extra})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, res
}

func readAnswer(t *testing.T, root, task string) schemas.AnswerSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "benchmarks", "lme", "tasks", task, "verifier", "answer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var a schemas.AnswerSpec
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

func readFile(t *testing.T, path ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRegistered(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("longmemeval importer not registered")
	}
	info := im.Info()
	if info.License != "MIT" || info.Track != schemas.TrackMemory || info.Grading != schemas.GradingJudged {
		t.Fatalf("info %+v", info)
	}
}

func TestImportJudged(t *testing.T) {
	root, res := runImport(t, nil, nil)
	want := []string{"fx-temporal-1", "fx-count-1", "fx-pref-1", "fx-update-1-abs", "fx-user-1"}
	if !reflect.DeepEqual(res.Tasks, want) || len(res.Skipped) != 0 {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	dir := filepath.Join(root, "benchmarks", "lme")
	if got := readFile(t, dir, ".gitignore"); !strings.Contains(got, "*") {
		t.Fatalf(".gitignore %q", got)
	}
	var s struct {
		Grader, Grading, Track string
		Judge                  schemas.JudgeSpec
		Tasks                  []string
		Imported               map[string]string
	}
	if err := yaml.Unmarshal([]byte(readFile(t, dir, "suite.yaml")), &s); err != nil {
		t.Fatal(err)
	}
	if s.Grader != "judge" || s.Grading != "judged" || s.Track != "memory" || !reflect.DeepEqual(s.Tasks, want) {
		t.Fatalf("suite %+v", s)
	}
	if s.Judge.Provider != "pin-me" || s.Judge.Model != "pin-me" || s.Judge.PromptVersion != "longmemeval-v1" {
		t.Fatalf("judge %+v", s.Judge)
	}
	if s.Imported["importer"] != Name || s.Imported["license"] != "MIT" || s.Imported["benchmark"] != "longmemeval-cleaned@oracle" || s.Imported["at"] != "2026-11-15T08:00:00Z" {
		t.Fatalf("imported %v", s.Imported)
	}
	if len(res.Notes) < 3 || !strings.Contains(res.Notes[0], "gpt-4o-2024-08-06") || !strings.Contains(res.Notes[1], "tokens per question") {
		t.Fatalf("notes %v", res.Notes)
	}

	temporal := readAnswer(t, root, "fx-temporal-1")
	if temporal.Category != "temporal-reasoning" || temporal.Question == "" || !reflect.DeepEqual(temporal.Expected, []string{"The blue toaster"}) {
		t.Fatalf("temporal spec %+v", temporal)
	}
	if !reflect.DeepEqual(temporal.WrongOnes, []string{"I don't know. That was never mentioned in the conversations.", "red kettle"}) {
		t.Fatalf("wrong answers %q", temporal.WrongOnes)
	}
	if count := readAnswer(t, root, "fx-count-1"); !reflect.DeepEqual(count.Expected, []string{"3"}) || count.Match != "" || len(count.WrongOnes) != 2 || count.WrongOnes[1] != "8" {
		t.Fatalf("count spec %+v", count)
	}
	if pref := readAnswer(t, root, "fx-pref-1"); pref.Rubric == "" || len(pref.Expected) != 0 {
		t.Fatalf("preference spec %+v", pref)
	}
	abs := readAnswer(t, root, "fx-update-1-abs")
	if !abs.Abstain || len(abs.WrongOnes) != 0 {
		t.Fatalf("abstention spec %+v", abs)
	}
	if ref := readFile(t, dir, "tasks", "fx-update-1-abs", "solution", "answer.txt"); !strings.Contains(ref, "don't know") {
		t.Fatalf("abstention reference %q", ref)
	}
}

func TestImportSessionsAndNoLeaks(t *testing.T) {
	root, _ := runImport(t, map[string]string{"ingest_sessions": "2"}, []string{"fx_temporal_1"})
	task := filepath.Join(root, "benchmarks", "lme", "tasks", "fx-temporal-1")
	// Three sessions in two parts: 2 then 1, in date order.
	first := readFile(t, task, "sessions", "s1-files", "history", "conversation-001.md")
	second := readFile(t, task, "sessions", "s1-files", "history", "conversation-002.md")
	third := readFile(t, task, "sessions", "s2-files", "history", "conversation-003.md")
	if !strings.Contains(first, "2024/03/02") || !strings.Contains(first, "blue toaster") || !strings.Contains(second, "2024/03/07") || !strings.Contains(third, "red kettle") {
		t.Fatalf("history not in date order:\n%s\n%s\n%s", first, second, third)
	}
	if _, err := os.Stat(filepath.Join(task, "sessions", "s3.md")); err == nil {
		t.Fatal("more ingest sessions than asked for")
	}
	q := readFile(t, task, "sessions", "question.md")
	if !strings.Contains(q, "2024/03/20 (Wed) 09:00") || !strings.Contains(q, "red kettle or the blue toaster") || !strings.Contains(q, "answer.txt") {
		t.Fatalf("question instruction:\n%s", q)
	}
	err := filepath.WalkDir(task, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(p, "verifier") || strings.Contains(p, "solution") || strings.Contains(p, "controls") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, leak := range []string{"has_answer", "answer_fx1", "fx1_filler", "The blue toaster"} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s leaks %q", p, leak)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var ty struct {
		Sessions []schemas.SessionSpec
		Output   schemas.OutputSpec
		Controls schemas.TaskControls
	}
	if err := yaml.Unmarshal([]byte(readFile(t, task, "task.yaml")), &ty); err != nil {
		t.Fatal(err)
	}
	if len(ty.Sessions) != 3 || ty.Sessions[2].ID != "question" || ty.Sessions[2].Files != "" || ty.Sessions[0].Files != "sessions/s1-files" || ty.Output.Kind != "answer" {
		t.Fatalf("task sessions %+v output %+v", ty.Sessions, ty.Output)
	}
	if ty.Controls.Reference != "solution/answer.txt" || len(ty.Controls.Mutants) != 0 || ty.Controls.Exploit != "controls/exploit.sh" {
		t.Fatalf("controls %+v", ty.Controls)
	}
}

func TestImportMatch(t *testing.T) {
	root, res := runImport(t, map[string]string{"grading": "match"}, nil)
	if !reflect.DeepEqual(res.Tasks, []string{"fx-count-1", "fx-update-1-abs"}) {
		t.Fatalf("tasks %v", res.Tasks)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.ID] = s.Reason
	}
	if !strings.Contains(reasons["fx_temporal_1"], "free-text") || !strings.Contains(reasons["fx_pref_1"], "rubric") || !strings.Contains(reasons["fx_user_1"], "free-text") {
		t.Fatalf("skip reasons %v", reasons)
	}
	if count := readAnswer(t, root, "fx-count-1"); count.Match != "numeric" || !reflect.DeepEqual(count.Expected, []string{"3"}) {
		t.Fatalf("count spec %+v", count)
	}
	if abs := readAnswer(t, root, "fx-update-1-abs"); !abs.Abstain || len(abs.Expected) != 0 {
		t.Fatalf("abstention spec %+v", abs)
	}
	suiteYAML := readFile(t, root, "benchmarks", "lme", "suite.yaml")
	if !strings.Contains(suiteYAML, "grader: answer_match") || strings.Contains(suiteYAML, "grading:") || strings.Contains(suiteYAML, "judge:") {
		t.Fatalf("match suite:\n%s", suiteYAML)
	}
}

func TestImportLintsClean(t *testing.T) {
	root, _ := runImport(t, nil, nil)
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "lme")
	if err != nil {
		t.Fatalf("load imported suite: %v", err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error %s: %s", p.Path, p.Message)
		}
	}
}

func TestImportExtendAndRefuse(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: fixedNow}
	opts := importers.Options{BenchRoot: root, From: "testdata", SuiteID: "lme", IDs: []string{"fx_user_1"}}
	if _, err := im.Import(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.IDs = []string{"fx_count_1", "fx_user_1", "nope"}
	res, err := im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Tasks, []string{"fx-count-1"}) || len(res.Skipped) != 2 {
		t.Fatalf("second import tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	if got := readFile(t, root, "benchmarks", "lme", "suite.yaml"); !strings.Contains(got, "- fx-user-1\n  - fx-count-1") {
		t.Fatalf("suite tasks not extended in order:\n%s", got)
	}
	opts.Extra = map[string]string{"grading": "match"}
	opts.IDs = []string{"fx_update_1_abs"}
	if _, err := im.Import(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "new suite id") {
		t.Fatalf("mixing match into a judged suite: err = %v", err)
	}
}

func TestImportSettings(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]string
		want  string
	}{
		{"unknown key", map[string]string{"colour": "red"}, "unknown setting"},
		{"bad variant", map[string]string{"variant": "m"}, "want oracle or s"},
		{"bad sessions", map[string]string{"ingest_sessions": "0"}, "whole number"},
		{"bad grading", map[string]string{"grading": "vibes"}, "judged or match"},
		{"missing s file", map[string]string{"variant": "s"}, "no s file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			im := &importer{now: fixedNow}
			_, err := im.Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: "testdata", SuiteID: "x", Extra: tt.extra})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestOtherOption(t *testing.T) {
	tests := []struct{ q, a, want string }{
		{"Which vehicle did I take care of first in February, the bike or the car?", "bike", "car"},
		{"Which event did I attend first, the 'Time' workshop or the 'Data' webinar?", "'Data' webinar", "'Time' workshop"},
		{"Who did I meet first, Mark and Sarah or Tom?", "Tom", "Mark and Sarah"},
		{"Which came first, the red one or the blue one?", "both", ""},
		{"What did I buy?", "a hat", ""},
		{"Which first, A or B or C?", "A", ""},
	}
	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			if got := otherOption(tt.q, tt.a); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestImportRealData imports questions from a local copy of the upstream
// dataset and lints the suite. It runs only when BENCH_LONGMEMEVAL_FROM names
// the folder or file (BENCH_LONGMEMEVAL_VARIANT oracle or s, default oracle;
// BENCH_LONGMEMEVAL_OUT keeps the bench root instead of a temporary one).
func TestImportRealData(t *testing.T) {
	from := os.Getenv("BENCH_LONGMEMEVAL_FROM")
	if from == "" {
		t.Skip("BENCH_LONGMEMEVAL_FROM not set")
	}
	root := os.Getenv("BENCH_LONGMEMEVAL_OUT")
	if root == "" {
		root = t.TempDir()
	}
	extra := map[string]string{"variant": "oracle"}
	if v := os.Getenv("BENCH_LONGMEMEVAL_VARIANT"); v != "" {
		extra["variant"] = v
	}
	for _, grading := range []string{"judged", "match"} {
		t.Run(grading, func(t *testing.T) {
			extra["grading"] = grading
			id := "lme-" + extra["variant"] + "-" + grading
			im := &importer{now: time.Now}
			res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: id, Limit: 40, Force: true, Extra: extra})
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

// A refused extension (other grading) must leave the existing task's
// reference answer untouched, even under -force.
func TestRefusedImportKeepsExistingTask(t *testing.T) {
	root, _ := runImport(t, map[string]string{"grading": "match"}, []string{"fx_count_1"})
	answer := filepath.Join(root, "benchmarks", "lme", "tasks", "fx-count-1", "solution", "answer.txt")
	before := readFile(t, answer)
	if err := os.WriteFile(answer, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	im := &importer{now: fixedNow}
	_, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "lme",
		IDs: []string{"fx_count_1"}, Force: true, Extra: map[string]string{"grading": "judged"}})
	if err == nil || !strings.Contains(err.Error(), "new suite id") {
		t.Fatalf("judged into a match suite: err = %v", err)
	}
	if got := readFile(t, answer); got != "sentinel\n" {
		t.Fatalf("refused import rewrote the task (was %q, now %q)", before, got)
	}
}

// A truncated array with no closing bracket is refused unless a -limit
// deliberately stopped the scan early.
func TestTruncatedDatasetRefused(t *testing.T) {
	raw := readFile(t, "testdata", "longmemeval_oracle.json")
	dec := json.NewDecoder(strings.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "longmemeval_oracle.json")
	if err := os.WriteFile(file, append([]byte("[\n"), first...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEntries(context.Background(), file, nil, 0); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated array: err = %v", err)
	}
	if got, err := readEntries(context.Background(), file, nil, 1); err != nil || len(got) != 1 {
		t.Fatalf("limit 1 on truncated array: %d entries, err %v", len(got), err)
	}
	if err := os.WriteFile(file, append(append([]byte("[\n"), first...), "\n] x"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEntries(context.Background(), file, nil, 0); err == nil {
		t.Fatal("trailing data after the array was accepted")
	}
}
