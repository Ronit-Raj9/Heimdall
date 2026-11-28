package taskguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const testSHA = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func okChecks() []schemas.AdmissionCheck {
	return []schemas.AdmissionCheck{
		{Name: "reference_1", Want: "pass", Got: "pass", Passed: true},
		{Name: "reference_2", Want: "pass", Got: "pass", Passed: true},
		{Name: "reference_3", Want: "pass", Got: "pass", Passed: true},
		{Name: "empty_patch", Want: "fail", Got: "fail", Passed: true},
		{Name: "mutant_1", Want: "fail", Got: "fail", Passed: true},
		{Name: "null_noop", Want: "fail", Got: "fail", Passed: true},
	}
}

func okRec() *schemas.AdmissionRecord {
	return &schemas.AdmissionRecord{Admitted: true, Checks: okChecks(), ReferenceResults: []string{"pass", "pass", "pass"}}
}

// traceTask writes a reviewed trace task with a clean instruction.
func traceTask(t *testing.T, instruction string) (*schemas.Suite, *schemas.Task) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "t1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "instruction.md", instruction)
	yes := true
	task := &schemas.Task{ID: "t1", Dir: dir, Instruction: "instruction.md",
		Provenance: &schemas.TaskProvenance{CreatedBy: "task from-trace", Source: "trace a.jsonl (claude) " + testSHA, InstructionReviewed: &yes}}
	return &schemas.Suite{ID: "s", Root: root}, task
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func check(t *testing.T, rec *schemas.AdmissionRecord, name string) schemas.AdmissionCheck {
	t.Helper()
	for _, c := range rec.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %s missing from %+v", name, rec.Checks)
	return schemas.AdmissionCheck{}
}

func TestApplyIgnoresHandWritten(t *testing.T) {
	s, task := traceTask(t, "Fix the bug.")
	task.Provenance = nil
	rec := okRec()
	n := len(rec.Checks)
	got := Apply(s, task, rec)
	if len(got.Checks) != n || !got.Admitted {
		t.Fatalf("hand-written task changed: %+v", got)
	}
	task.Provenance = &schemas.TaskProvenance{CreatedBy: "task init"}
	if got := Apply(s, task, okRec()); len(got.Checks) != n {
		t.Fatalf("task init task guarded: %+v", got.Checks)
	}
}

func TestApplyCleanTraceTaskAdmitted(t *testing.T) {
	s, task := traceTask(t, "Help the customer with their order.")
	rec := Apply(s, task, okRec())
	if !rec.Admitted {
		t.Fatalf("clean task not admitted: %+v", rec.Checks)
	}
	for _, name := range Checks() {
		if c := check(t, rec, name); !c.Passed {
			t.Errorf("%s failed: %+v", name, c)
		}
	}
}

func TestApplyNoopPassesFails(t *testing.T) {
	s, task := traceTask(t, "Help the customer.")
	rec := okRec()
	for i := range rec.Checks {
		if rec.Checks[i].Name == "null_noop" {
			rec.Checks[i].Got, rec.Checks[i].Passed = "pass", false
		}
	}
	rec.Admitted = true // pretend admission let it through
	got := Apply(s, task, rec)
	c := check(t, got, CheckNoop)
	if c.Passed || got.Admitted || c.Detail != "An agent that does nothing passes this task." {
		t.Fatalf("noop = %+v admitted %v", c, got.Admitted)
	}
	rec = &schemas.AdmissionRecord{Admitted: true, Checks: []schemas.AdmissionCheck{{Name: "reference_1", Passed: true}}}
	if c := check(t, Apply(s, task, rec), CheckNoop); c.Passed {
		t.Fatal("no do-nothing check in rec must fail")
	}
	if c := check(t, rec, CheckControl); c.Passed {
		t.Fatal("no mutant or exploit check in rec must fail")
	}
}

func TestApplyLeak(t *testing.T) {
	s, task := traceTask(t, "Answer the customer.\nTell them: the refund takes 5 business  days.\n")
	if err := Write(task.Dir, Record{Builder: BuilderTrace, LeakTexts: []string{"The refund takes 5 business days"}}); err != nil {
		t.Fatal(err)
	}
	rec := Apply(s, task, okRec())
	c := check(t, rec, CheckLeak)
	if c.Passed || rec.Admitted || c.Detail != "The instruction or the workspace holds the answer: instruction.md, line 2." {
		t.Fatalf("leak = %+v", c)
	}
}

func TestApplyLeakInWorkspaceAndIssueDiff(t *testing.T) {
	s, task := traceTask(t, "Fix the parser.")
	task.Provenance = &schemas.TaskProvenance{CreatedBy: "heimdall issuebench emit"}
	task.RepoDir = "repo"
	task.Controls.Reference = "solution/reference.diff"
	write(t, task.Dir, "solution/reference.diff", "--- a/p.go\n+++ b/p.go\n@@\n+\treturn parseNumber(tokenStream)\n+x\n")
	write(t, task.Dir, "repo/notes.txt", "hint\n// return parseNumber(tokenStream)\n")
	c := check(t, Apply(s, task, okRec()), CheckLeak)
	if c.Passed || !strings.Contains(c.Detail, "repo/notes.txt, line 2") {
		t.Fatalf("leak = %+v", c)
	}
}

func TestApplyFlakyFromRecord(t *testing.T) {
	s, task := traceTask(t, "Help.")
	if err := Write(task.Dir, Record{Builder: BuilderTrace, FlakyTests: []string{"test_a", "test_b"}}); err != nil {
		t.Fatal(err)
	}
	c := check(t, Apply(s, task, okRec()), CheckFlaky)
	if c.Passed || c.Detail != "Tests that flip between runs: test_a, test_b." {
		t.Fatalf("flaky = %+v", c)
	}
	rec := okRec()
	rec.Checks[1].Passed, rec.Checks[1].Got = false, "fail"
	os.Remove(filepath.Join(task.Dir, File))
	c = check(t, Apply(s, task, rec), CheckFlaky)
	if c.Passed || c.Detail != "The reference does not pass every time (2 of 3)." {
		t.Fatalf("flaky = %+v", c)
	}
}

func TestApplyQuarantined(t *testing.T) {
	s, task := traceTask(t, "Help.")
	write(t, s.Root, schemas.SuiteHealthFileName, `{"suite":"s","quarantined":{"t1":{"reason":"flaky","source":"auto"}}}`)
	rec := Apply(s, task, okRec())
	if c := check(t, rec, CheckFlaky); c.Passed || rec.Admitted {
		t.Fatalf("quarantined task passed: %+v", c)
	}
}

func TestApplyDeterminism(t *testing.T) {
	s, task := traceTask(t, "Help.")
	rec := okRec()
	rec.ReferenceResults = []string{"pass", "fail", "pass"}
	c := check(t, Apply(s, task, rec), CheckDeterminism)
	if c.Passed || c.Detail != "The reference got different scores on the same code: pass, fail, pass." {
		t.Fatalf("determinism = %+v", c)
	}
}

func TestApplyClarityNeedsReviewOrClear(t *testing.T) {
	s, task := traceTask(t, "Do the thing.")
	unclear := Record{Builder: BuilderTrace, Clarity: &Clarity{Verdict: ClarityUnclear, Origin: OriginModel,
		Problems: []ClarityProblem{{Quote: "the thing", Problem: "Which thing?"}}}}
	if err := Write(task.Dir, unclear); err != nil {
		t.Fatal(err)
	}
	if c := check(t, Apply(s, task, okRec()), CheckClarity); !c.Passed {
		t.Fatalf("reviewed instruction failed: %+v", c)
	}
	no := false
	task.Provenance.InstructionReviewed = &no
	c := check(t, Apply(s, task, okRec()), CheckClarity)
	if c.Passed || c.Detail != "The instruction may be unclear: the thing. A person must approve the instruction: open the draft and press Approve." {
		t.Fatalf("clarity = %+v", c)
	}
	unclear.Clarity.Verdict = ClarityClear
	if err := Write(task.Dir, unclear); err != nil {
		t.Fatal(err)
	}
	if c := check(t, Apply(s, task, okRec()), CheckClarity); !c.Passed {
		t.Fatalf("clear verdict failed: %+v", c)
	}
}

func TestApplyProvenanceTrace(t *testing.T) {
	s, task := traceTask(t, "Help.")
	task.Provenance.Source = "trace a.jsonl"
	if c := check(t, Apply(s, task, okRec()), CheckProvenance); c.Passed || c.Detail != "Where this task came from is not recorded." {
		t.Fatalf("provenance = %+v", c)
	}
}

func TestApplyProvenanceIssue(t *testing.T) {
	s, task := traceTask(t, "Help.")
	sha := strings.Repeat("a", 40)
	task.Provenance = &schemas.TaskProvenance{CreatedBy: "heimdall issuebench emit", Source: "https://github.com/o/r/issues/1", BaseCommit: sha, ReferenceCommit: sha}
	if c := check(t, Apply(s, task, okRec()), CheckProvenance); !c.Passed {
		t.Fatalf("issue provenance = %+v", c)
	}
	task.Provenance.BaseCommit = "abc"
	if c := check(t, Apply(s, task, okRec()), CheckProvenance); c.Passed {
		t.Fatal("short base commit passed")
	}
	task.Provenance.BaseCommit, task.Provenance.ReferenceCommit = "", ""
	if err := Write(task.Dir, Record{Builder: BuilderIssue, Source: sha + ".." + strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	if c := check(t, Apply(s, task, okRec()), CheckProvenance); !c.Passed {
		t.Fatalf("commit pair from guard.json not used: %+v", c)
	}
}

func TestApplyProvenanceAPI(t *testing.T) {
	s, task := traceTask(t, "Help.")
	task.Provenance = &schemas.TaskProvenance{CreatedBy: "api policy generator", Source: "app task draft d1"}
	if c := check(t, Apply(s, task, okRec()), CheckProvenance); !c.Passed {
		t.Fatalf("api provenance = %+v", c)
	}
	task.Provenance = &schemas.TaskProvenance{CreatedBy: "policy generator (m)"}
	if c := check(t, Apply(s, task, okRec()), CheckProvenance); c.Passed {
		t.Fatal("policy task without source passed")
	}
}

func TestApplyPrivacyCountsOnly(t *testing.T) {
	s, task := traceTask(t, "Write to jane.doe@example.com about the order.")
	c := check(t, Apply(s, task, okRec()), CheckPrivacy)
	if c.Passed || !strings.Contains(c.Detail, "1 email") || strings.Contains(c.Detail, "jane") {
		t.Fatalf("privacy = %+v", c)
	}
}

func TestApplyDerivesLeakTextsWithoutGuardFile(t *testing.T) {
	s, task := traceTask(t, "What is the capital? It is Ulaanbaatar City.")
	task.Verifier.Answer = "verifier/answer.yaml"
	write(t, task.Dir, "verifier/answer.yaml", "match: exact\nexpected: [\"Ulaanbaatar City\"]\n")
	write(t, task.Dir, "verifier/checks.yaml", "version: 1\nchecks:\n  - id: c\n    tool_called: {tool: refund, args: {order_id: ORDER-LONG-12345}}\n")
	rec := Apply(s, task, okRec())
	c := check(t, rec, CheckLeak)
	if c.Passed || rec.Admitted || !strings.Contains(c.Detail, "instruction.md, line 1") {
		t.Fatalf("leak = %+v", c)
	}
	got := derivedLeakTexts(task)
	if len(got) != 2 {
		t.Fatalf("derived = %q", got)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if r, err := Read(dir); r != nil || err != nil {
		t.Fatalf("missing file = %v %v", r, err)
	}
	if err := Write(dir, Record{Builder: BuilderAPI, Source: "d1"}); err != nil {
		t.Fatal(err)
	}
	r, err := Read(dir)
	if err != nil || r.Builder != BuilderAPI || r.WrittenAt.IsZero() {
		t.Fatalf("read = %+v %v", r, err)
	}
}
