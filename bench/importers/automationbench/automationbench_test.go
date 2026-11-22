package automationbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/suite"
)

func TestImport(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: func() time.Time { return time.Unix(0, 0) }}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/ds", SuiteID: "automationbench",
		Extra: map[string]string{"references": "testdata/refs", "personas": "testdata/personas"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 2 || len(res.Skipped) != 0 {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	buf := filepath.Join(res.SuiteDir, "tasks", "ab-simple-buffer-twitter-product-launch")
	fin := filepath.Join(res.SuiteDir, "tasks", "ab-finance-slack-receipt-capture")
	for _, p := range []string{"task.yaml", "instruction.md", "scenario/scenario.json", "verifier/tests/grading.json", "controls/reference.sh", "controls/empty.sh"} {
		if _, err := os.Stat(filepath.Join(buf, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	y, _ := os.ReadFile(filepath.Join(buf, "task.yaml"))
	for _, want := range []string{"id: apps", `image: "bench/automationbench:1"`, `verifier_image: "bench/automationbench:1"`, "path: /state/world.json", `start: "` + DefaultClock + `"`, `"app-buffer"`} {
		if !strings.Contains(string(y), want) {
			t.Errorf("task.yaml lacks %q:\n%s", want, y)
		}
	}
	fy, _ := os.ReadFile(filepath.Join(fin, "task.yaml"))
	if !strings.Contains(string(fy), "/task/personas.json: scenario/personas.json") || strings.Contains(string(y), "personas.json") {
		t.Errorf("personas file only on the task that has one:\n%s", fy)
	}
	if fi, _ := os.ReadFile(filepath.Join(fin, "instruction.md")); !strings.Contains(string(fi), "Priya Shah, Finance lead, priya@company.example.com") {
		t.Errorf("instruction lacks the colleagues: %s", fi)
	}
	if !strings.Contains(string(fy), "clock:\n  start: \"2026-") {
		t.Errorf("finance task clock should come from meta.current_time:\n%s", fy)
	}
	sc, _ := os.ReadFile(filepath.Join(buf, "scenario/scenario.json"))
	if strings.Contains(string(sc), "assertions") || !strings.Contains(string(sc), `"toolset": "api"`) {
		t.Errorf("scenario must hold no assertions and name the toolset: %s", sc)
	}
	g, _ := os.ReadFile(filepath.Join(buf, "verifier/tests/grading.json"))
	if !strings.Contains(string(g), "buffer_post_exists") {
		t.Errorf("grading.json lacks the assertions: %s", g)
	}
	ref, _ := os.ReadFile(filepath.Join(buf, "controls/reference.sh"))
	if !strings.Contains(string(ref), "def fetch(") || !strings.Contains(string(ref), "bufferapp.com") {
		t.Errorf("reference not built from references: %s", ref)
	}
	fref, _ := os.ReadFile(filepath.Join(fin, "controls/reference.sh"))
	if !strings.Contains(string(fref), "exit 1") {
		t.Errorf("task without a reference must refuse: %s", fref)
	}
	ins, _ := os.ReadFile(filepath.Join(buf, "instruction.md"))
	if !strings.Contains(string(ins), "NexGen 2.0 Launch") || !strings.Contains(string(ins), "MCP server `apps`") {
		t.Errorf("instruction: %s", ins)
	}
	for _, p := range []string{"Dockerfile", "abench.py", "personas.py", "stubs/datasets/__init__.py", "stubs/openai/__init__.py"} {
		if _, err := os.Stat(filepath.Join(res.SuiteDir, "services", "automationbench", p)); err != nil {
			t.Errorf("service build file %s: %v", p, err)
		}
	}
	for _, id := range res.Tasks {
		s, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "automationbench", id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if task.Clock == nil {
			t.Errorf("%s: no clock", id)
		}
		for _, pr := range suite.LintTask(s, task) {
			if pr.Severity == suite.SeverityError {
				t.Errorf("%s lint error: %s: %s", id, pr.Path, pr.Message)
			}
		}
	}
}

func TestSettingsAndIDs(t *testing.T) {
	if TaskID("sales", "sales.multi_hop_lookup") != "ab-sales-multi-hop-lookup" {
		t.Error(TaskID("sales", "sales.multi_hop_lookup"))
	}
	st, err := parseSettings(map[string]string{"domains": "public", "toolset": ToolsetLimited})
	if err != nil || len(st.domains) != 6 || st.domains["simple"] || st.toolset != ToolsetLimited {
		t.Fatalf("%+v %v", st, err)
	}
	for _, bad := range []map[string]string{{"domains": "legal"}, {"toolset": "zapier"}, {"nope": "1"}} {
		if _, err := parseSettings(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	root := t.TempDir()
	im := &importer{now: time.Now}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/ds", SuiteID: "ab-simple", Extra: map[string]string{"domains": "simple"}})
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("domain filter: %v %v", res, err)
	}
}
