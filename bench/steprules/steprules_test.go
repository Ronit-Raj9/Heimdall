package steprules

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func ip(v int) *int { return &v }

func cmd(seq int, text string, proof schemas.EvidenceStrength, exit int) schemas.StepRecord {
	return schemas.StepRecord{Seq: seq, Kind: schemas.StepCommand, Summary: text, Proof: proof, ExitCode: ip(exit)}
}

func turn(seq int, summary string) schemas.StepRecord {
	return schemas.StepRecord{Seq: seq, Kind: schemas.StepModelTurn, Summary: summary, Proof: schemas.EvidenceVerified}
}

func builtin(t *testing.T, id string) *Compiled {
	t.Helper()
	for _, r := range Builtin() {
		if r.ID == id {
			c, err := Compile(r)
			if err != nil {
				t.Fatalf("compile %s: %v", id, err)
			}
			return c
		}
	}
	t.Fatalf("no built-in rule %s", id)
	return nil
}

func TestBuiltinClaimedWithoutTestsFires(t *testing.T) {
	steps := []schemas.StepRecord{
		turn(1, "I will look at the code"),
		{Seq: 2, Kind: schemas.StepFileChange, Path: "src/app.py", Op: "write", Proof: schemas.EvidenceVerified},
		turn(9, "The bug is fixed."),
	}
	res := builtin(t, "claimed_without_tests").Eval(steps, false)
	if res.Status != StatusFired || len(res.Hits) != 1 || len(res.Hits[0].Seqs) != 1 || res.Hits[0].Seqs[0] != 9 {
		t.Fatalf("got %+v", res)
	}
}

func TestBuiltinClaimedWithoutTestsClearWhenWitnessedTestBefore(t *testing.T) {
	steps := []schemas.StepRecord{
		turn(1, "start"),
		cmd(5, "go test ./...", schemas.EvidenceVerified, 0),
		turn(9, "All tests pass, done."),
	}
	res := builtin(t, "claimed_without_tests").Eval(steps, false)
	if res.Status != StatusClear {
		t.Fatalf("got %+v", res)
	}
}

func TestRequestedOnlyTestRunDoesNotCount(t *testing.T) {
	steps := []schemas.StepRecord{
		{Seq: 3, Kind: schemas.StepToolRequest, ToolName: "bash", Summary: "pytest -q", Execution: schemas.ExecutionNoWitness, Proof: schemas.EvidencePartial},
		turn(7, "Fixed it."),
	}
	res := builtin(t, "claimed_without_tests").Eval(steps, false)
	if res.Status != StatusFired {
		t.Fatalf("got %+v", res)
	}
}

func TestRepeatNeedsSameKey(t *testing.T) {
	c := builtin(t, "repeats_failed_command")
	same := []schemas.StepRecord{
		cmd(1, "pytest -x tests/test_a.py --seed 101", schemas.EvidenceVerified, 1),
		turn(2, "again"),
		cmd(3, "pytest -x tests/test_a.py --seed 202", schemas.EvidenceVerified, 1),
		cmd(4, "pytest -x tests/test_a.py --seed 303", schemas.EvidenceVerified, 1),
	}
	res := c.Eval(same, false)
	if res.Status != StatusFired || len(res.Hits) != 1 || len(res.Hits[0].Seqs) != 3 {
		t.Fatalf("same key: %+v", res)
	}
	mixed := []schemas.StepRecord{
		cmd(1, "pytest a.py", schemas.EvidenceVerified, 1),
		cmd(2, "pytest b.py", schemas.EvidenceVerified, 1),
		cmd(3, "pytest a.py", schemas.EvidenceVerified, 1),
	}
	if res := c.Eval(mixed, false); res.Status != StatusClear {
		t.Fatalf("mixed: %+v", res)
	}
}

func TestAfterErrorWindow(t *testing.T) {
	c := builtin(t, "ignores_tool_error")
	base := []schemas.StepRecord{
		{Seq: 4, Kind: schemas.StepToolExecution, ToolName: "read_file", Path: "src/a.py", IsError: true, Proof: schemas.EvidenceVerified},
		{Seq: 5, Kind: schemas.StepFileChange, Path: "docs/b.md", Op: "write", Proof: schemas.EvidenceVerified},
		cmd(6, "ls", schemas.EvidenceVerified, 0),
	}
	if res := c.Eval(base, false); res.Status != StatusFired || res.Hits[0].Seqs[0] != 4 {
		t.Fatalf("unrelated: %+v", res)
	}
	fixed := []schemas.StepRecord{
		base[0],
		{Seq: 5, Kind: schemas.StepToolRequest, ToolName: "view", Path: "src/a.py", Proof: schemas.EvidencePartial},
		cmd(6, "ls", schemas.EvidenceVerified, 0),
	}
	if res := c.Eval(fixed, false); res.Status != StatusClear {
		t.Fatalf("read of same path: %+v", res)
	}
}

func TestAbsentUnknownOnTruncated(t *testing.T) {
	c, err := Compile(Rule{ID: "never_tests", Title: "Never runs tests", Kind: KindAbsent, A: Selector{Class: ClassTestRun}, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	res := c.Eval([]schemas.StepRecord{cmd(1, "ls", schemas.EvidenceVerified, 0)}, true)
	if res.Status != StatusUnknown || res.Reason != "the step timeline was cut at 20000 rows" {
		t.Fatalf("got %+v", res)
	}
	if res := c.Eval([]schemas.StepRecord{cmd(1, "ls", schemas.EvidenceVerified, 0)}, false); res.Status != StatusFired {
		t.Fatalf("not truncated: %+v", res)
	}
}

func TestCompileRejects(t *testing.T) {
	ok := Rule{ID: "my_rule", Title: "My rule", Kind: KindPresent, A: Selector{Class: ClassEdit}, Version: 1}
	cases := []struct {
		edit func(r *Rule)
		want string
	}{
		{func(r *Rule) { r.Kind = "sometimes" }, "rule kind must be one of present, absent, sequence, before, repeat, after_error"},
		{func(r *Rule) { r.ID = "My-Rule" }, "rule id must start with a lowercase letter and hold only lowercase letters, digits and underscores, 2 to 48 characters"},
		{func(r *Rule) { r.Title = "" }, "rule title is required"},
		{func(r *Rule) { r.Title = strings.Repeat("x", 81) }, "rule title must be at most 80 characters"},
		{func(r *Rule) { r.A.Class = "nap" }, "rule a: class must be one of test_run, edit, read, shell, tool_error, success_claim, model_turn, retry"},
		{func(r *Rule) { r.A.Command = "(" }, "rule a: command is not a valid pattern"},
		{func(r *Rule) { r.A.Command = strings.Repeat("a", 257) }, "rule a: command must be at most 256 characters"},
		{func(r *Rule) { r.A.Path = "[" }, "rule a: path is not a valid file pattern"},
		{func(r *Rule) { r.A.Op = "move" }, "rule a: op must be one of create, write, delete, rename"},
		{func(r *Rule) { r.A.Kinds = []string{"nap"} }, "rule a: kinds must be step kinds such as command, file_change, tool_request"},
		{func(r *Rule) { r.Kind = KindSequence }, "rule b is required for kind sequence"},
		{func(r *Rule) { r.Kind = KindBefore }, "rule b is required for kind before"},
		{func(r *Rule) { r.Kind = KindRepeat; r.N = 1 }, "rule n must be between 2 and 50"},
		{func(r *Rule) { r.Kind = KindAfterError; r.Window = 51 }, "rule window must be between 1 and 50"},
	}
	if _, err := Compile(ok); err != nil {
		t.Fatalf("valid rule: %v", err)
	}
	for _, tc := range cases {
		r := ok
		tc.edit(&r)
		_, err := Compile(r)
		if err == nil || err.Error() != tc.want {
			t.Errorf("want %q, got %v", tc.want, err)
		}
	}
}

func TestKeyNormalizes(t *testing.T) {
	a := Key(cmd(1, "cat /tmp/abc123/x", schemas.EvidenceVerified, 0))
	b := Key(cmd(2, "cat /tmp/zzz999/x", schemas.EvidenceVerified, 0))
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
	if Key(cmd(1, "git show deadbeef12", "", 0)) != Key(cmd(1, "git show 0123abcd99", "", 0)) {
		t.Fatal("hex runs not normalized")
	}
	if Key(turn(1, "x")) != "turn" {
		t.Fatal("turn key")
	}
}

func TestDefaultClassesKeepsPatternEventsWords(t *testing.T) {
	src, err := os.ReadFile("../metrics/patternevents.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var testCommands = \[\]string\{([^}]*)\}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("testCommands not found in metrics/patternevents.go")
	}
	have := map[string]bool{}
	for _, w := range DefaultClasses().TestCommands {
		have[w] = true
	}
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1) {
		if !have[string(q[1])] {
			t.Errorf("DefaultClasses().TestCommands lacks %q", q[1])
		}
	}
}

func TestPresentPathGlobAndWitnessed(t *testing.T) {
	c := builtin(t, "edits_protected_tests")
	steps := []schemas.StepRecord{
		{Seq: 1, Kind: schemas.StepFileChange, Path: "pkg/tests/unit/test_x.py", Op: "write", Proof: schemas.EvidenceVerified},
		{Seq: 2, Kind: schemas.StepFileChange, Path: "src/a.go", Op: "write", Proof: schemas.EvidenceVerified},
		{Seq: 3, Kind: schemas.StepFileChange, Path: "src/a_test.go", Op: "write", Proof: schemas.EvidencePartial},
	}
	res := c.Eval(steps, false)
	if res.Status != StatusFired || len(res.Hits) != 1 || res.Hits[0].Seqs[0] != 1 || !res.Hits[0].Witnessed {
		t.Fatalf("got %+v", res)
	}
}

func TestNoTimelineIsUnknown(t *testing.T) {
	res := builtin(t, "reads_git_history").Eval(nil, false)
	if res.Status != StatusUnknown || res.Reason == "" {
		t.Fatalf("got %+v", res)
	}
}

func TestLoadMissingAndTruncated(t *testing.T) {
	steps, trunc, err := Load(func(string) ([]byte, error) { return nil, os.ErrNotExist }, 0)
	if steps != nil || trunc || err != nil {
		t.Fatalf("missing: %v %v %v", steps, trunc, err)
	}
	data := []byte("{\"seq\":2,\"kind\":\"command\"}\n{\"seq\":1,\"kind\":\"command\"}\n{\"seq\":3,\"kind\":\"command\"}\n")
	steps, trunc, err = Load(func(string) ([]byte, error) { return data, nil }, 2)
	if err != nil || !trunc || len(steps) != 2 || steps[0].Seq != 1 {
		t.Fatalf("truncated: %v %v %v", steps, trunc, err)
	}
	costs, err := LoadCost(func(string) ([]byte, error) {
		return []byte(`[{"request_id":"r1","usd":{"state":"observed","v":0.5}},{"request_id":"r1","usd":{"state":"observed","v":0.25}},{"request_id":"r2","usd":{"state":"unknown","v":null}}]`), nil
	})
	if err != nil || costs["r1"] != 0.75 {
		t.Fatalf("cost: %v %v", costs, err)
	}
	if _, ok := costs["r2"]; ok {
		t.Fatal("unknown cost kept")
	}
}
