package gaiascore

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testdata/official-outcomes.json holds what the official scorer
// (question_scorer of scorer.py in the GAIA leaderboard space, revision
// 9f133d71) returned for each pair on 2026-11-18. The scorer itself is not
// vendored; TestAgainstOfficialScorer reruns it when GAIA_SCORER names a
// local copy.
type outcome struct {
	ModelAnswer string `json:"model_answer"`
	GroundTruth string `json:"ground_truth"`
	Correct     bool   `json:"correct"`
}

func loadOutcomes(t *testing.T) []outcome {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "official-outcomes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []outcome
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) < 50 {
		t.Fatalf("only %d outcomes", len(out))
	}
	return out
}

func TestScoreMatchesOfficialOutcomes(t *testing.T) {
	for _, c := range loadOutcomes(t) {
		if got := Score(c.ModelAnswer, c.GroundTruth); got != c.Correct {
			t.Errorf("Score(%q, %q) = %v, official scorer said %v", c.ModelAnswer, c.GroundTruth, got, c.Correct)
		}
	}
}

// TestAgainstOfficialScorer runs the official scorer on the same pairs when
// GAIA_SCORER points at a local copy of scorer.py, so a change upstream
// shows up as a failure here.
func TestAgainstOfficialScorer(t *testing.T) {
	scorer := os.Getenv("GAIA_SCORER")
	if scorer == "" {
		t.Skip("GAIA_SCORER not set (path to the leaderboard's scorer.py)")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	in, _ := json.Marshal(loadOutcomes(t))
	prog := `import io, contextlib, json, sys, warnings
warnings.simplefilter("ignore")
ns = {}
exec(open(sys.argv[1]).read().replace("import numpy as np\n", ""), ns)
out = []
for c in json.load(sys.stdin):
    with contextlib.redirect_stdout(io.StringIO()):
        out.append(bool(ns["question_scorer"](c["model_answer"], c["ground_truth"])))
print(json.dumps(out))`
	cmd := exec.Command("python3", "-I", "-B", "-c", prog, scorer)
	cmd.Stdin = bytes.NewReader(in)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("official scorer: %v", err)
	}
	var got []bool
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for i, c := range loadOutcomes(t) {
		if got[i] != Score(c.ModelAnswer, c.GroundTruth) {
			t.Errorf("pair %q / %q: official %v, ours %v", c.ModelAnswer, c.GroundTruth, got[i], !got[i])
		}
	}
}

func TestPyFloat(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"17", 17, true}, {" 17\n", 17, true}, {"+1.5", 1.5, true}, {"-2", -2, true}, {"1_000", 1000, true},
		{"1__0", 0, false}, {"_1", 0, false}, {"1_", 0, false}, {"1_.5", 0, false}, {".5", 0.5, true}, {"5.", 5, true},
		{".", 0, false}, {"1e3", 1000, true}, {"1E-2", 0.01, true}, {"1e", 0, false}, {"e3", 0, false},
		{"0x10", 0, false}, {"0b1", 0, false}, {"１２", 12, true}, {"٤٢", 42, true}, {"Infinity", math.Inf(1), true},
		{"-inf", math.Inf(-1), true}, {"1e999", math.Inf(1), true}, {"", 0, false}, {"1,000", 0, false}, {"$5", 0, false},
	}
	for _, c := range cases {
		got, ok := PyFloat(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("PyFloat(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if f, ok := PyFloat("NaN"); !ok || !math.IsNaN(f) {
		t.Errorf("PyFloat(NaN) = %v, %v", f, ok)
	}
}

func TestKind(t *testing.T) {
	for truth, want := range map[string]string{"17": "number", "1e3": "number", "a, b": "list", "1,000": "list", "x; y": "list", "Paris": "string", "$5": "string"} {
		if got := Kind(truth); got != want {
			t.Errorf("Kind(%q) = %s, want %s", truth, got, want)
		}
	}
}

func TestExtract(t *testing.T) {
	cases := map[string]string{
		"Paris\n":                                       "Paris",
		"I think so.\nFINAL ANSWER: Paris":              "Paris",
		"final answer: 17\nmore text":                   "17",
		"FINAL ANSWER: 1\nchecking...\nFINAL ANSWER: 2": "2",
		"**Final Answer:** apple, banana":               "apple, banana",
		"The final answer is Paris":                     "The final answer is Paris",
	}
	for in, want := range cases {
		if got := Extract(in); got != want {
			t.Errorf("Extract(%q) = %q, want %q", in, got, want)
		}
	}
}
