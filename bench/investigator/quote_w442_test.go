package investigator

import (
	"strings"
	"testing"
)

func TestNormalizeFolds(t *testing.T) {
	cases := []struct{ in, want string }{
		{"“quoted” and ‘single’", `"quoted" and 'single'`},
		{"a **bold** and _em_ and `code`", "a bold and em and code"},
		{`line one\nline two\t"x\"`, `line one line two "x"`},
		{"...the middle part…", "the middle part"},
		{"  many   spaces\n\nhere  ", "many spaces here"},
		{"ＡBC", "ABC"},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func stepSource(steps map[int]string) func(string, int) (string, bool) {
	return func(runID string, seq int) (string, bool) {
		if runID != "r1" {
			return "", false
		}
		s, ok := steps[seq]
		return s, ok
	}
}

func TestCheckQuotesExactAfterNormalize(t *testing.T) {
	src := stepSource(map[int]string{3: "bash: the command `pytest -q` exited with status 2 because a fixture was missing"})
	res := CheckQuotes([]Quote{{RunID: "r1", Seq: 3, Text: "the command **pytest -q** exited with status 2..."}}, src)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("want the quote to pass, got %+v", res)
	}
}

func TestCheckQuotesRejectsParaphrase(t *testing.T) {
	step := "the agent edited config.yaml and then deleted the test file before running the suite"
	src := stepSource(map[int]string{4: step})
	res := CheckQuotes([]Quote{{RunID: "r1", Seq: 4, Text: "the agent edited config.yaml and then removed the test file"}}, src)
	if res[0].OK || res[0].Reason != "not found in the step" {
		t.Fatalf("a paraphrase passed: %+v", res[0])
	}
	if !strings.Contains(res[0].Closest, "deleted the test file") {
		t.Fatalf("closest %q does not hold the real passage", res[0].Closest)
	}
}

func TestCheckQuotesNoSuchStep(t *testing.T) {
	src := stepSource(map[int]string{1: "some step text here"})
	res := CheckQuotes([]Quote{{RunID: "r1", Seq: 9, Text: "some step text here"}, {RunID: "r2", Seq: 1, Text: "some step text here"}}, src)
	for _, r := range res {
		if r.OK || r.Reason != "no such step" {
			t.Fatalf("want no such step, got %+v", r)
		}
	}
}

func TestCheckQuotesLength(t *testing.T) {
	long := strings.Repeat("b", 241)
	src := stepSource(map[int]string{1: "abcdefg " + long})
	res := CheckQuotes([]Quote{{RunID: "r1", Seq: 1, Text: "abcdefg"}, {RunID: "r1", Seq: 1, Text: long}, {RunID: "r1", Seq: 1, Text: "abcdefg b"}}, src)
	if res[0].OK || res[0].Reason != "too short" {
		t.Fatalf("7 chars: %+v", res[0])
	}
	if res[1].OK || res[1].Reason != "too long" {
		t.Fatalf("241 chars: %+v", res[1])
	}
	if !res[2].OK {
		t.Fatalf("9 chars: %+v", res[2])
	}
}
