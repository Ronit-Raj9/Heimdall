package bigfinancebench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

func TestImportFixture(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "bigfinancebench"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 1 || res.Tasks[0] != "bf-00000000aa" {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "no rubric" {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	dir := filepath.Join(res.SuiteDir, "tasks", "bf-00000000aa")
	raw, err := os.ReadFile(filepath.Join(dir, "verifier", "answer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var ans schemas.AnswerSpec
	if err := yaml.Unmarshal(raw, &ans); err != nil {
		t.Fatal(err)
	}
	lines, err := graders.ParseRubricLines(ans.Rubric)
	if err != nil || len(lines) != 2 || lines[1].Points != 5 {
		t.Fatalf("rubric %q: %v %v", ans.Rubric, lines, err)
	}
	if ans.Expected[0] != "41.27%" || len(ans.WrongOnes) != 2 || ans.WrongOnes[0] != "57.54%" {
		t.Fatalf("answer spec %+v", ans)
	}
	suite, err := os.ReadFile(filepath.Join(res.SuiteDir, "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"grader: judge", "prompt_version: bigfinancebench-v1", "model: pin-me"} {
		if !strings.Contains(string(suite), want) {
			t.Errorf("suite.yaml lacks %q", want)
		}
	}
	task, _ := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if !strings.Contains(string(task), "id: websearch") {
		t.Error("task has no websearch service")
	}
}

func TestNearMiss(t *testing.T) {
	for in, want := range map[string]string{"$14.21": "$20.47", "102 bps": "141 bps", "no number": ""} {
		if got := nearMiss(in); got != want {
			t.Errorf("nearMiss(%q) = %q, want %q", in, got, want)
		}
	}
}
