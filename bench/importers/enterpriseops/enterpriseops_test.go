package enterpriseops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
)

func TestImport(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: func() time.Time { return time.Unix(0, 0) }}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/ds", SuiteID: "enterpriseops-gym", Extra: map[string]string{"references": "testdata/refs"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 2 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "response_check") {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	hy := filepath.Join(res.SuiteDir, "tasks", TaskID("hybrid", "task_B_2"))
	for _, p := range []string{"task.yaml", "instruction.md", "service/email/seed.sql", "service/hr/checks.json", "verifier/tests/checks.json", "verifier/tests/runverify.py", "controls/reference.sh"} {
		if _, err := os.Stat(filepath.Join(hy, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	y, _ := os.ReadFile(filepath.Join(hy, "task.yaml"))
	for _, want := range []string{"id: email", "id: hr", `image: "bench/eog-hr:1"`, "/bench/task/seed.sql: service/hr/seed.sql", "argv: [python, /bench/bench_front.py, export]"} {
		if !strings.Contains(string(y), want) {
			t.Errorf("task.yaml lacks %q:\n%s", want, y)
		}
	}
	ref, _ := os.ReadFile(filepath.Join(hy, "controls/reference.sh"))
	if !strings.Contains(string(ref), "hand reference") {
		t.Errorf("reference not taken from references: %s", ref)
	}
	ins, _ := os.ReadFile(filepath.Join(hy, "instruction.md"))
	if !strings.Contains(string(ins), "Turn POP on.") || !strings.Contains(string(ins), "Ann") {
		t.Errorf("instruction: %s", ins)
	}
	df, err := os.ReadFile(filepath.Join(res.SuiteDir, "services", "eog-hr", "Dockerfile"))
	if err != nil || !strings.Contains(string(df), "enterpriseops-gym-mcp-hr:latest") {
		t.Errorf("hr Dockerfile: %v %s", err, df)
	}
	if TaskID("email", "task_20251125_150631_436_e5bf1251_77dd7cbb") != "eog-email-20251125-150631-436-e5bf1251-77dd7cbb" {
		t.Error("TaskID")
	}
}
