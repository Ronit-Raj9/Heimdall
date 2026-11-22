package webarenapro

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
	im := &importer{now: func() time.Time { return time.Unix(0, 0) }}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/export", SuiteID: "wap"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 2 || res.Tasks[0] != "dev-001" {
		t.Fatalf("tasks %v", res.Tasks)
	}
	dir := filepath.Join(root, "benchmarks", "wap", "tasks")
	ref, _ := os.ReadFile(filepath.Join(dir, "dev-001", "controls", "reference.sh"))
	if !strings.Contains(string(ref), `ANSWER = "Osaka; Jeju"`) {
		t.Fatalf("reference: %s", ref)
	}
	ref2, _ := os.ReadFile(filepath.Join(dir, "dev-002", "controls", "reference.sh"))
	if !strings.Contains(string(ref2), "exit 1") {
		t.Fatalf("dev-002 reference should refuse: %s", ref2)
	}
	ty, _ := os.ReadFile(filepath.Join(dir, "dev-001", "task.yaml"))
	for _, want := range []string{"id: webarena", "role: mcp", "hidden: true", "flight-search", "agent_s: 900", "webarena-pro-harbor finalize"} {
		if !strings.Contains(string(ty), want) {
			t.Errorf("task.yaml lacks %q:\n%s", want, ty)
		}
	}
	if strings.Contains(string(ty), "gateway:") {
		t.Error("answer-only task must not ask for the gateway")
	}
	ty2, _ := os.ReadFile(filepath.Join(dir, "dev-002", "task.yaml"))
	if !strings.Contains(string(ty2), "model: gpt-6.2-sol") {
		t.Error("judged task lacks the pinned judge gateway")
	}
}

func TestReferenceExact(t *testing.T) {
	var ep episode
	ep.Task.Eval.Nodes = append(ep.Task.Eval.Nodes, ep.Task.Eval.Nodes...)
	if _, why := referenceAnswer(ep); why == "" {
		t.Fatal("empty rubric must refuse")
	}
}
