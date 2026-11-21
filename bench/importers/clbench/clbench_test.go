package clbench

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/suite"
)

const fixtureRef = "5f8c50eb1e84b2eda2ef4faff757dfc812a0ea26"

func fixtureImport(t *testing.T, extra map[string]string) (*importers.Result, string) {
	t.Helper()
	root := t.TempDir()
	if extra == nil {
		extra = map[string]string{}
	}
	extra[SetRef] = fixtureRef
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{
		BenchRoot: root, From: filepath.Join("testdata", "upstream"), SuiteID: "cl-bench", Extra: extra,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, root
}

func TestImportStreams(t *testing.T) {
	res, root := fixtureImport(t, nil)
	if !slices.Equal(res.Tasks, []string{"clb-poker-default", "clb-sales-default"}) {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != "clb-poker-quick-test" {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	bench := filepath.Join(root, "benchmarks")
	for _, id := range res.Tasks {
		s, task, err := suite.LoadTask(bench, "cl-bench", id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		for _, p := range suite.LintTask(s, task) {
			if p.Severity == suite.SeverityError {
				t.Errorf("%s lint: %+v", id, p)
			}
		}
		if len(task.Services) != 1 || task.Services[0].ID != serviceID || task.Services[0].Role != "mcp" {
			t.Fatalf("%s services = %+v", id, task.Services)
		}
	}
	_, poker, err := suite.LoadTask(bench, "cl-bench", "clb-poker-default")
	if err != nil {
		t.Fatal(err)
	}
	if len(poker.Sessions) != 2 || poker.Sessions[0].WallS != minSessionS {
		t.Fatalf("poker sessions = %+v", poker.Sessions)
	}
	env := poker.Services[0].Env
	if env["CLBENCH_STAGES"] != `[{"label":"Calling Station","instances":3},{"label":"Fit Or Fold","instances":2}]` || env["CLBENCH_FAMILY"] != "exploitable_poker" {
		t.Fatalf("service env = %v", env)
	}
	dir := filepath.Join(bench, "cl-bench", "tasks", "clb-poker-default")
	ref, err := os.ReadFile(filepath.Join(dir, "controls", "reference.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ref), env["CLBENCH_REFERENCE_TOKEN"]) || strings.Contains(string(ref), "__SESSIONS__") {
		t.Fatal("reference control does not carry the service token and session count")
	}
	for _, f := range []string{"service/clbench/src/interface.py", "service/clbench/LICENSE", "service/clbench/data/exploitable_poker/hands.json", "service/server.py", "sessions/session-2.md", "verifier/tests/runverify.py"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	sales, err := os.ReadFile(filepath.Join(bench, "cl-bench", "tasks", "clb-sales-default", "controls", "reference.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sales), "draft: ") || !strings.Contains(string(sales), "exit 1") {
		t.Fatalf("sales reference is not a refusing draft:\n%s", sales)
	}
	y, err := os.ReadFile(filepath.Join(bench, "cl-bench", "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(y), "pass_threshold: 0.5") || !strings.Contains(string(y), "track: memory") {
		t.Fatalf("suite.yaml:\n%s", y)
	}
}

func TestSettings(t *testing.T) {
	if _, err := parseSettings("x", map[string]string{"nope": "1"}); err == nil {
		t.Fatal("unknown setting accepted")
	}
	res, _ := fixtureImport(t, map[string]string{SetSmoke: "true", SetSchedules: "all"})
	if !slices.Contains(res.Tasks, "clb-poker-quick-test") {
		t.Fatalf("smoke schedule not imported: %v", res.Tasks)
	}
}
