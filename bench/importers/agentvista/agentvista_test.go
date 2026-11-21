package agentvista

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
)

func TestImportFixture(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/fixture", SuiteID: "agentvista"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Tasks, ",") != "av-000,av-001" {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != "av-002" || !strings.Contains(res.Skipped[0].Reason, "missing") {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	suite := filepath.Join(root, "benchmarks", "agentvista")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(suite, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	s := read("suite.yaml")
	for _, want := range []string{"grader: judge", "prompt_version: agentvista-v1", "provider: pin-me", "grading: judged"} {
		if !strings.Contains(s, want) {
			t.Errorf("suite.yaml lacks %q", want)
		}
	}
	ty := read("tasks/av-001/task.yaml")
	for _, want := range []string{"draft", "multi-image", "visual-location-identification", "websearch", "cassettes/tavily.cassette.jsonl"} {
		if !strings.Contains(ty, want) {
			t.Errorf("av-001 task.yaml lacks %q", want)
		}
	}
	if strings.Contains(read("tasks/av-000/task.yaml"), " draft") {
		t.Error("av-000 must not be a draft")
	}
	read("tasks/av-001/DRAFT.md")
	read("tasks/av-001/repo/images/av-001-2.png")
	if got := read("tasks/av-000/solution/answer.txt"); got != "<answer>3</answer>\n" {
		t.Errorf("reference = %q", got)
	}
	if a := read("tasks/av-000/verifier/answer.yaml"); !strings.Contains(a, "- \"3\"") && !strings.Contains(a, "- '3'") {
		t.Errorf("answer.yaml = %s", a)
	}
	if !strings.Contains(read("tasks/av-000/instruction.md"), "images/av-000-1.png") {
		t.Error("instruction does not name the image")
	}
	// A second import keeps a pinned judge.
	os.WriteFile(filepath.Join(suite, "suite.yaml"), []byte(strings.Replace(strings.Replace(s, "provider: pin-me", "provider: openai", 1), "model: pin-me", "model: m1", 1)), 0o644)
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/fixture", SuiteID: "agentvista", Force: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read("suite.yaml"), "model: m1") {
		t.Error("re-import dropped the pinned judge")
	}
}

func TestSettings(t *testing.T) {
	if _, err := parseSettings(map[string]string{"bogus": "1"}); err == nil {
		t.Error("unknown setting accepted")
	}
	if _, err := parseSettings(map[string]string{"version": "x"}); err == nil {
		t.Error("bad version accepted")
	}
}
