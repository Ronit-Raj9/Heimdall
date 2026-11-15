package graders

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// answerRC builds a run context for an answer task whose hidden spec is
// specYAML (written to verifier/answer.yaml unless empty) and whose run
// folder holds answer (no answer.txt when answer is nil).
func answerRC(t *testing.T, specYAML string, answer *string) *schemas.RunContext {
	t.Helper()
	taskDir := t.TempDir()
	if specYAML != "" {
		if err := os.MkdirAll(filepath.Join(taskDir, "verifier"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(taskDir, "verifier", "answer.yaml"), []byte(specYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runDir := t.TempDir()
	if answer != nil {
		if err := os.WriteFile(filepath.Join(runDir, AnswerFile), []byte(*answer), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &schemas.RunContext{
		Suite: &schemas.Suite{ID: "mem", Version: "1.2.3", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}},
		Task:  &schemas.Task{ID: "q1", Dir: taskDir, Verifier: schemas.TaskVerifier{Tests: "verifier"}},
		Run:   &schemas.Run{RunID: "r_abc123"},
		Dir:   runDir,
	}
}

func strp(s string) *string { return &s }

func TestAnswerMatchGrade(t *testing.T) {
	const norm = "normalize: [lower, strip_punct, collapse_space, trim]\n"
	tests := []struct {
		name     string
		spec     string
		answer   *string
		pass     bool
		invalid  bool
		category string
		reason   string // substring of Grade.Reason
	}{
		{name: "exact normalized", spec: "match: exact\nexpected: [\"United Airlines\"]\ncategory: temporal-reasoning\n" + norm,
			answer: strp("  united   airlines.\n"), pass: true, category: "temporal-reasoning"},
		{name: "exact any of several", spec: "match: exact\nexpected: [Paris, \"Paris, France\"]\n" + norm, answer: strp("Paris, France"), pass: true},
		{name: "exact without normalizers is strict", spec: "match: exact\nexpected: [Paris]\n", answer: strp("paris"), reason: "exact: answer does not match"},
		{name: "exact wrong", spec: "match: exact\nexpected: [Paris]\n" + norm, answer: strp("Lyon"), reason: `got "Lyon"`},
		{name: "contains_all", spec: "match: contains_all\nexpected: [red, blue]\n" + norm, answer: strp("Blue and RED."), pass: true},
		{name: "contains_all missing one", spec: "match: contains_all\nexpected: [red, blue]\n" + norm, answer: strp("red only"), reason: "contains_all"},
		{name: "regex", spec: "match: regex\nexpected: ['^\\d{4}-\\d{2}-\\d{2}$']\nnormalize: [trim]\n", answer: strp(" 2023-05-07 \n"), pass: true},
		{name: "regex no match", spec: "match: regex\nexpected: ['^\\d{4}$']\n", answer: strp("May 2023")},
		{name: "numeric digits", spec: "match: numeric\nexpected: [\"4\"]\n", answer: strp("I went to 4 events."), pass: true},
		{name: "numeric word", spec: "match: numeric\nexpected: [\"4\"]\n", answer: strp("Four."), pass: true},
		{name: "numeric thousands", spec: "match: numeric\nexpected: [\"12500\"]\n", answer: strp("$12,500"), pass: true},
		{name: "numeric tolerance", spec: "match: numeric\nexpected: [\"18\"]\ntolerance: 1\n", answer: strp("19 days"), pass: true},
		{name: "numeric outside tolerance", spec: "match: numeric\nexpected: [\"18\"]\ntolerance: 1\n", answer: strp("20 days"), reason: "not within 1"},
		{name: "numeric two numbers is ambiguous", spec: "match: numeric\nexpected: [\"4\"]\n", answer: strp("4 or 5"), reason: "2 different numbers"},
		{name: "numeric repeated number is one", spec: "match: numeric\nexpected: [\"4\"]\n", answer: strp("4. Yes, 4."), pass: true},
		{name: "numeric none", spec: "match: numeric\nexpected: [\"4\"]\n", answer: strp("several"), reason: "no number"},
		{name: "choice label", spec: "match: choice\nexpected: [B]\n", answer: strp("(b) Not mentioned"), pass: true},
		{name: "choice answer prefix", spec: "match: choice\nexpected: [B]\n", answer: strp("Answer: B"), pass: true},
		{name: "choice wrong label", spec: "match: choice\nexpected: [B]\n", answer: strp("A."), reason: "choice"},
		{name: "choice word is not a label", spec: "match: choice\nexpected: [B]\n", answer: strp("bike")},
		{name: "choice option text", spec: "match: choice\nexpected: [\"the bike\"]\n" + norm, answer: strp("The bike!"), pass: true},
		{name: "json equal", spec: "match: json\nexpected: ['{\"a\": 1, \"b\": [1, 2]}']\n", answer: strp(`{"b":[1,2.0],"a":1}`), pass: true},
		{name: "json different", spec: "match: json\nexpected: ['{\"a\": 1}']\n", answer: strp(`{"a":2}`), reason: "json: answer does not match"},
		{name: "json not json", spec: "match: json\nexpected: ['{\"a\": 1}']\n", answer: strp(`a=1`), reason: "not one JSON value"},
		{name: "json trailing data", spec: "match: json\nexpected: ['1']\n", answer: strp(`1 2`), reason: "trailing data"},
		{name: "abstain passes", spec: "abstain: true\ncategory: adversarial\n", answer: strp("I don’t know; you never told me."), pass: true, category: "adversarial"},
		{name: "abstain locomo phrase", spec: "abstain: true\n", answer: strp("Not mentioned in the conversation"), pass: true},
		{name: "abstain wrong", spec: "abstain: true\nexpected: [x]\nmatch: exact\n", answer: strp("Her dog is called Max."), reason: "expected the answer to say it is not known"},
		{name: "missing answer fails", spec: "match: exact\nexpected: [a]\n", answer: nil, reason: "no answer: answer.txt is not in the run folder"},
		{name: "empty answer fails", spec: "match: exact\nexpected: [a]\n", answer: strp(" \n\t"), reason: "answer.txt is empty"},
		{name: "missing spec is invalid", spec: "", answer: strp("a"), invalid: true, reason: "answer spec"},
		{name: "unknown mode is invalid", spec: "match: fuzzy\nexpected: [a]\n", answer: strp("a"), invalid: true, reason: "unknown mode"},
		{name: "unknown key is invalid", spec: "match: exact\nexpected: [a]\nexpect: [b]\n", answer: strp("a"), invalid: true, reason: "expect"},
		{name: "bad regex is invalid", spec: "match: regex\nexpected: ['(']\n", answer: strp("a"), invalid: true, reason: "expected[0]"},
		{name: "bad number is invalid", spec: "match: numeric\nexpected: [four]\n", answer: strp("4"), invalid: true, reason: "not a number"},
		{name: "unknown normalizer is invalid", spec: "match: exact\nexpected: [a]\nnormalize: [stem]\n", answer: strp("a"), invalid: true, reason: "stem"},
		{name: "negative tolerance is invalid", spec: "match: numeric\nexpected: [\"1\"]\ntolerance: -1\n", answer: strp("1"), invalid: true, reason: "tolerance"},
		{name: "no expected is invalid", spec: "match: exact\n", answer: strp("a"), invalid: true, reason: "expected: empty"},
		{name: "empty match is invalid", spec: "expected: [a]\n", answer: strp("a"), invalid: true, reason: "match: empty"},
	}
	g := newAnswerMatch()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := answerRC(t, tt.spec, tt.answer)
			got, err := g.Grade(context.Background(), rc, &schemas.Diff{SHA256: "d1"})
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if got.Passed != tt.pass || got.InvalidEvaluator != tt.invalid {
				t.Fatalf("passed=%v invalid=%v (reason %q), want passed=%v invalid=%v", got.Passed, got.InvalidEvaluator, got.Reason, tt.pass, tt.invalid)
			}
			if !strings.Contains(got.Reason, tt.reason) {
				t.Fatalf("reason %q does not contain %q", got.Reason, tt.reason)
			}
			if got.Mode != GradeModeAnswer || got.GraderVersion != answerMatchVersion || got.SuiteVersion != "1.2.3" || got.DiffSHA256 != "d1" {
				t.Fatalf("grade header %+v", got)
			}
			if got.Category != tt.category || got.Judged || got.Judge != nil {
				t.Fatalf("category %q judged %v, want %q and not judged", got.Category, got.Judged, tt.category)
			}
		})
	}
}

func TestAnswerMatchReasonNeverQuotesExpected(t *testing.T) {
	rc := answerRC(t, "match: exact\nexpected: [s3cret-answer]\n", strp("wrong"))
	got, err := newAnswerMatch().Grade(context.Background(), rc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Reason, "s3cret") {
		t.Fatalf("reason leaks the expected answer: %q", got.Reason)
	}
}

func TestAnswerMatchRefusesNonAnswerTask(t *testing.T) {
	rc := answerRC(t, "match: exact\nexpected: [a]\n", strp("a"))
	rc.Suite.Output = nil
	if _, err := newAnswerMatch().Grade(context.Background(), rc, nil); err == nil || !strings.Contains(err.Error(), "output kind diff") {
		t.Fatalf("err = %v, want output kind refusal", err)
	}
	if _, err := newAnswerMatch().Grade(context.Background(), &schemas.RunContext{}, nil); err == nil {
		t.Fatal("no error for a run context without a task")
	}
}

func TestAnswerMatchOversizedAnswerFails(t *testing.T) {
	big := strings.Repeat("a", maxAnswerBytes+1)
	rc := answerRC(t, "match: exact\nexpected: [a]\n", &big)
	got, err := newAnswerMatch().Grade(context.Background(), rc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Passed || got.InvalidEvaluator || !strings.Contains(got.Reason, "larger than") {
		t.Fatalf("grade %+v", got)
	}
}

func TestAnswerSpecPath(t *testing.T) {
	tests := []struct {
		name string
		v    schemas.TaskVerifier
		want string
	}{
		{"explicit", schemas.TaskVerifier{Tests: "verifier", Answer: "hidden/a.yaml"}, "hidden/a.yaml"},
		{"tests folder", schemas.TaskVerifier{Tests: "grader-files"}, "grader-files/answer.yaml"},
		{"default", schemas.TaskVerifier{}, "verifier/answer.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnswerSpecPath(&schemas.Task{Verifier: tt.v}); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLoadAnswerSpecStaysInsideTask(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "answer.yaml"), []byte("match: exact\nexpected: [a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskDir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(taskDir, "verifier")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		task *schemas.Task
	}{
		{"symlink out", &schemas.Task{Dir: taskDir, Verifier: schemas.TaskVerifier{Tests: "verifier"}}},
		{"dot dot", &schemas.Task{Dir: taskDir, Verifier: schemas.TaskVerifier{Answer: "../answer.yaml"}}},
		{"absolute", &schemas.Task{Dir: taskDir, Verifier: schemas.TaskVerifier{Answer: filepath.Join(outside, "answer.yaml")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadAnswerSpec(tt.task); err == nil {
				t.Fatal("spec outside the task folder was read")
			}
		})
	}
}

func TestSaysUnknown(t *testing.T) {
	tests := []struct {
		answer string
		want   bool
	}{
		{"I don't know.", true},
		{"I do not   know", true},
		{"That was never mentioned.", true},
		{"No information available", true},
		{"It cannot be determined from our chats.", true},
		{"Max", false},
		{"You told me about your dog Max.", false},
	}
	for _, tt := range tests {
		t.Run(tt.answer, func(t *testing.T) {
			if got := SaysUnknown(tt.answer); got != tt.want {
				t.Fatalf("SaysUnknown(%q) = %v", tt.answer, got)
			}
		})
	}
}

// TestAnswerMatchControlsOfSuite grades every task's answer controls in an
// answer suite on disk with answer_match, as admission would: the reference
// must pass; the empty answer, every mutant, every wrong_answers entry and
// the exploit (a script, run with BENCH_ANSWER_PATH set) must fail. It runs
// only when BENCH_ANSWER_SUITE names the suite folder (for example one
// written by `bifrost-bench import longmemeval -set grading=match`).
func TestAnswerMatchControlsOfSuite(t *testing.T) {
	dir := os.Getenv("BENCH_ANSWER_SUITE")
	if dir == "" {
		t.Skip("BENCH_ANSWER_SUITE not set")
	}
	tasks, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	g := newAnswerMatch()
	for _, e := range tasks {
		taskDir := filepath.Join(dir, "tasks", e.Name())
		var tc struct {
			Controls schemas.TaskControls `yaml:"controls"`
			Verifier schemas.TaskVerifier `yaml:"verifier"`
		}
		raw, err := os.ReadFile(filepath.Join(taskDir, "task.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(raw, &tc); err != nil {
			t.Fatal(err)
		}
		task := &schemas.Task{ID: e.Name(), Dir: taskDir, Verifier: tc.Verifier}
		spec, err := LoadAnswerSpec(task)
		if err != nil {
			t.Fatal(err)
		}
		type control struct {
			name, answer string
			pass         bool
		}
		read := func(rel string) string {
			b, err := os.ReadFile(filepath.Join(taskDir, rel))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		controls := []control{{"reference", read(tc.Controls.Reference), true}, {"empty", read(tc.Controls.Empty), false}}
		for _, m := range tc.Controls.Mutants {
			controls = append(controls, control{m, read(m), false})
		}
		for i, w := range spec.WrongOnes {
			controls = append(controls, control{fmt.Sprintf("wrong_answers[%d]", i), w, false})
		}
		out := filepath.Join(t.TempDir(), AnswerFile)
		cmd := exec.Command("sh", filepath.Join(taskDir, tc.Controls.Exploit))
		cmd.Dir, cmd.Env = t.TempDir(), append(os.Environ(), "BENCH_ANSWER_PATH="+out)
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: run exploit: %v", e.Name(), err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		controls = append(controls, control{"exploit", string(b), false})
		for _, c := range controls {
			answer := c.answer
			rc := answerRC(t, "", &answer)
			rc.Task = task
			got, err := g.Grade(context.Background(), rc, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.InvalidEvaluator || got.Passed != c.pass {
				t.Errorf("%s %s: passed=%v invalid=%v (%s), want passed=%v", e.Name(), c.name, got.Passed, got.InvalidEvaluator, got.Reason, c.pass)
			}
		}
	}
	t.Logf("graded the controls of %d tasks", len(tasks))
}
