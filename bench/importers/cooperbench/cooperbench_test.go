package cooperbench

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
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "cb", Extra: map[string]string{"revision": "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 1 || res.Tasks[0] != "demo-task7-f1-f2" {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	dir := filepath.Join(res.SuiteDir, "tasks", "demo-task7-f1-f2")
	ty, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"image: \"akhatua/cooperbench-demo:task7\"", "{id: agent1, role: lead}", "{id: agent2, role: worker}", "base_commit: \"0123456789abcdef0123456789abcdef01234567\""} {
		if !strings.Contains(string(ty), want) {
			t.Errorf("task.yaml lacks %q", want)
		}
	}
	for _, f := range []string{"verifier/tests/verify.py", "verifier/tests/tests1.patch", "verifier/tests/tests2.patch", "verifier/tests/runner.sh", "solution/reference.sh", "controls/mutant-one-feature.sh", "instruction.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "solution/reference.sh"))
	if !strings.Contains(string(ref), "def f2()") || !strings.Contains(string(ref), "!= agent1") {
		t.Error("reference must apply combined.patch, lead only")
	}
	mut, _ := os.ReadFile(filepath.Join(dir, "controls/mutant-one-feature.sh"))
	if strings.Contains(string(mut), "def f2()") {
		t.Error("mutant must hold feature 1 only")
	}
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata", SuiteID: "cb", IDs: []string{"nope"}}); err != nil {
		t.Fatal(err)
	}
}

func TestBadSetting(t *testing.T) {
	if _, err := parseSettings(map[string]string{"agent_s": "5"}); err == nil {
		t.Fatal("want error")
	}
}
