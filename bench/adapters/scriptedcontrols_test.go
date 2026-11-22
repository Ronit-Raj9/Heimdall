package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The hosted readiness controls run the task's own empty control, mutants
// and wrong answers in the agent's place, following its control kind; an
// index the task does not have changes nothing.
func TestScriptedReadinessControls(t *testing.T) {
	write := func(t *testing.T, dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	answer := &schemas.OutputSpec{Kind: schemas.OutputAnswer, AnswerPath: "answer.txt"}
	tests := []struct {
		name, kind string
		index      int
		output     *schemas.OutputSpec
		wantArgv   string
		wantFile   string
	}{
		{"answer empty", ScriptedEmpty, 0, answer, "sh -c", ""},
		{"answer mutant", ScriptedMutant, 1, answer, "sh -c", "Paris is in Spain.\n"},
		{"wrong answer 2", ScriptedWrongAnswer, 2, answer, "sh -c", "31 days"},
		{"missing wrong answer", ScriptedWrongAnswer, 3, answer, "echo " + ScriptedNoControl, ""},
		{"missing mutant", ScriptedMutant, 2, answer, "echo " + ScriptedNoControl, ""},
		{"patch empty", ScriptedEmpty, 0, nil, "true", ""},
		{"patch mutant", ScriptedMutant, 1, nil, "git -c", "Paris is in Spain.\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent := scriptedAgent(tc.kind)
			agent.Scripted.Index = tc.index
			rc, cfg := fixture(t, agent)
			rc.Suite = &schemas.Suite{Workdir: "/work"}
			rc.Task.Output = tc.output
			rc.Task.Controls.Empty = "controls/empty.txt"
			rc.Task.Controls.Mutants = []string{"controls/mutant-1.txt"}
			rc.Task.Verifier.Tests = "verifier"
			write(t, rc.Task.Dir, "controls/empty.txt", "")
			write(t, rc.Task.Dir, "controls/mutant-1.txt", "Paris is in Spain.\n")
			write(t, rc.Task.Dir, "verifier/answer.yaml", "match: exact\nexpected: [30 days]\nwrong_answers: [I don't know.]\ngenerated_wrong_answers: [31 days]\n")
			plan, err := newScriptedAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(plan.Argv, " "); !strings.HasPrefix(got, tc.wantArgv) {
				t.Fatalf("argv %q, want prefix %q", got, tc.wantArgv)
			}
			if tc.wantFile != "" && plan.Files[controlFile] != tc.wantFile {
				t.Fatalf("files %v", plan.Files)
			}
			if plan.Network != "none" {
				t.Fatalf("network %s", plan.Network)
			}
		})
	}
	agent := scriptedAgent(ScriptedMutant)
	rc, cfg := fixture(t, agent)
	if _, err := newScriptedAdapter(cfg).Plan(context.Background(), rc); err == nil {
		t.Fatal("a mutant control without an index must be refused")
	}
}
