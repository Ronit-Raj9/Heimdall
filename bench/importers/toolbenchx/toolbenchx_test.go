package toolbenchx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
)

func runImport(t *testing.T, variant string) (*importers.Result, string) {
	t.Helper()
	root := t.TempDir()
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{
		BenchRoot: root, From: "testdata/upstream", SuiteID: "tbx",
		Extra: map[string]string{SetVariant: variant, SetRef: "abc1234"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, root
}

func TestImportClean(t *testing.T) {
	res, _ := runImport(t, VariantClean)
	want := []string{"mix-demo-topic-unit-mix-t1", "seq-demo-topic-unit-lookup-t1"}
	if strings.Join(res.Tasks, ",") != strings.Join(want, ",") {
		t.Fatalf("tasks %v, want %v", res.Tasks, want)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != "seq-demo-topic-unit-lookup-t2" {
		t.Fatalf("skipped %v", res.Skipped)
	}
	dir := filepath.Join(res.SuiteDir, "tasks", "mix-demo-topic-unit-mix-t1")
	scen, _ := os.ReadFile(filepath.Join(dir, "scenario", "scenario.json"))
	if !strings.Contains(string(scen), `"price_a"`) || strings.Contains(string(scen), "final_answer") {
		t.Fatalf("scenario: %s", scen)
	}
	exp, _ := os.ReadFile(filepath.Join(dir, "verifier", "tests", "expected.json"))
	if !strings.Contains(string(exp), `"final_answer": "A"`) {
		t.Fatalf("expected: %s", exp)
	}
	ty, _ := os.ReadFile(filepath.Join(dir, "task.yaml"))
	for _, s := range []string{"role: mcp", "/task/tools.py: scenario/tools.py", "id: answer.json", "verifier_image:"} {
		if !strings.Contains(string(ty), s) {
			t.Fatalf("task.yaml lacks %q:\n%s", s, ty)
		}
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "controls", "reference.sh"))
	if !strings.Contains(string(ref), `"submit":"A"`) {
		t.Fatalf("reference: %s", ref)
	}
	suite, _ := os.ReadFile(filepath.Join(res.SuiteDir, "suite.yaml"))
	if !strings.Contains(string(suite), "track: tool_use") || !strings.Contains(string(suite), `revision: "abc1234"`) {
		t.Fatalf("suite.yaml:\n%s", suite)
	}
}

func TestImportHazard(t *testing.T) {
	res, _ := runImport(t, VariantHazard)
	if len(res.Tasks) != 1 || res.Tasks[0] != "seq-demo-topic-unit-lookup-t1" {
		t.Fatalf("tasks %v", res.Tasks)
	}
	ty, _ := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", res.Tasks[0], "task.yaml"))
	if !strings.Contains(string(ty), "invocation-uncertainty") {
		t.Fatalf("task.yaml:\n%s", ty)
	}
	tools, _ := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", res.Tasks[0], "scenario", "tools.py"))
	if !strings.Contains(string(tools), "TimeoutError") {
		t.Fatal("hazard tools not copied")
	}
}

func TestServiceImageShared(t *testing.T) {
	res, _ := runImport(t, VariantClean)
	var tags []string
	for _, id := range res.Tasks {
		b, _ := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", id, "task.yaml"))
		i := strings.Index(string(b), "verifier_image: ")
		tags = append(tags, strings.SplitN(string(b)[i:], "\n", 2)[0])
	}
	if tags[0] != tags[1] {
		t.Fatalf("service image differs per task: %v", tags)
	}
}

func TestMutateAnswer(t *testing.T) {
	for in, want := range map[string]string{"42": "43", "47.23": "48.23", "Ada": "Ada (unverified)"} {
		if got := mutateAnswer(in); got != want {
			t.Errorf("mutateAnswer(%q) = %q, want %q", in, got, want)
		}
	}
}
