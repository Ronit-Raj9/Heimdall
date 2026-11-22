package traceimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

func switchToChecks(t *testing.T, suiteDir string) {
	t.Helper()
	p := filepath.Join(suiteDir, "suite.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	next, err := scaffold.SetChecksGrader(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, next, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedDraftIsFinished(t *testing.T) {
	tests := []struct {
		name, kind, data, session string
		replay                    bool
		guide                     Guide
		want                      []string // substrings of the graded file
	}{
		{"qa", KindQA, string(fixture(t, "otlp-support.json")), "5b8efff798038103d269b633813fc60c", false,
			Guide{Answers: []string{"The refund was issued"}, RecordedWrong: true}, []string{"match: exact", "The refund was issued"}},
		{"qa-golden", KindQA, string(fixture(t, "langfuse-trace.json")), "", true, Guide{UseRecorded: true}, []string{"expected:"}},
		{"memory", KindMemory, memoryChat, "", false, Guide{Answers: []string{"2026-05-04"}, RecordedWrong: true}, []string{"2026-05-04", "2026-04-09"}},
		{"tool-use", KindToolUse, string(fixture(t, "chat-transcript.json")), "", true,
			Guide{KeepTurns: true, Calls: []GuideCall{{Tool: "get_item", Args: map[string]any{"sku": "W-100"}}, {Tool: "adjust_stock", Args: map[string]any{"sku": "W-100", "delta": 12}}},
				Forbidden: []GuideCall{{Tool: "adjust_stock", Args: map[string]any{"sku": "W-101"}}}, Ordered: true},
			[]string{"tool_called", "tool_not_called", "call_order", "W-101"}},
		{"mcp-golden", KindMCP, string(fixture(t, "otlp-support.json")), "5b8efff798038103d269b633813fc60c", true, Guide{UseRecorded: true}, []string{"tool_called"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newSuite(t, "prod", tc.kind)
			dir := filepath.Join(root, "prod")
			if tc.kind == KindToolUse || tc.kind == KindMCP {
				switchToChecks(t, dir)
			}
			s, src := redactedSession(t, []byte(tc.data), tc.session)
			g := tc.guide
			res, err := WriteDraft(DraftOptions{SuiteDir: dir, ID: "guided", Session: s, Source: src, Replay: tc.replay, Guide: &g})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.EditMe) != 0 || !GuidedDone(res) {
				t.Fatalf("edit me left: %v", res.EditMe)
			}
			st, task, err := suite.LoadTask(root, "prod", "guided")
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range suite.ValidateTask(context.Background(), st, task) {
				if p.Severity == suite.SeverityError {
					t.Errorf("%s: %s", p.Path, p.Message)
				}
			}
			graded := filepath.Join(res.Dir, "verifier", "answer.yaml")
			if tc.kind == KindToolUse || tc.kind == KindMCP {
				graded = taskchecks.TaskFile(task)
				if graded == "" {
					t.Fatal("no checks.yaml")
				}
				if _, err := taskchecks.Parse([]byte(readText(t, graded))); err != nil {
					t.Fatalf("checks.yaml: %v", err)
				}
				if len(task.Controls.Mutants) == 0 {
					t.Errorf("no mutant controls: %+v", task.Controls)
				}
				ref := readText(t, filepath.Join(res.Dir, "solution", "reference.sh"))
				if strings.Contains(ref, scaffold.EditMeMarker) {
					t.Errorf("reference still has EDIT ME:\n%s", ref)
				}
			}
			body := readText(t, graded)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("%s lacks %q:\n%s", filepath.Base(graded), w, body)
				}
			}
			if tc.guide.KeepTurns && len(s.Requests()) > 1 {
				if instr := readText(t, filepath.Join(res.Dir, "instruction.md")); !strings.Contains(instr, "Later in the conversation") {
					t.Errorf("later turns dropped:\n%s", instr)
				}
			}
		})
	}
}

func TestGuidedDraftRefusals(t *testing.T) {
	failed := string(fixture(t, "otlp-support.json"))
	cases := []struct {
		kind  string
		guide Guide
		want  string
	}{
		{KindQA, Guide{}, "right answer"},
		{KindQA, Guide{UseRecorded: true}, "cannot be the expected answer"},
		{KindQA, Guide{Answers: []string{"x"}, Calls: []GuideCall{{Tool: "a"}}}, "graded on its answer"},
		{KindMCP, Guide{}, "at least one check"},
		{KindMCP, Guide{Answers: []string{"x"}}, "graded on its tool calls"},
		{KindMCP, Guide{Calls: []GuideCall{{Tool: " "}}}, "tool name"},
		{KindQA, Guide{Answers: []string{"x"}, Match: "fuzzy"}, "match"},
	}
	for _, c := range cases {
		root := newSuite(t, "prod", c.kind)
		s, src := redactedSession(t, []byte(failed), "5b8efff798038103d269b633813fc60c")
		g := c.guide
		_, err := WriteDraft(DraftOptions{SuiteDir: filepath.Join(root, "prod"), ID: "guided", Session: s, Source: src, Guide: &g})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %+v: err = %v, want %q", c.kind, c.guide, err, c.want)
		}
		if _, err := os.Stat(filepath.Join(root, "prod", "tasks", "guided")); err == nil {
			t.Errorf("%s %+v: a refused draft left its folder", c.kind, c.guide)
		}
	}
}
