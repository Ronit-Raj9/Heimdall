package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestReportCardRouteCheatCheck: the report card route answers the cheat
// rate row: no twins without a twin benchmark, not measured once one has an
// admitted twin.
func TestReportCardRouteCheatCheck(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := schemas.Evaluation{ID: "ev_1", Status: "done", CreatedAt: time.Now(),
		Spec:     schemas.EvaluationSpec{Mode: "versions", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Version: 1}, {Label: "B", Agent: "codex", Version: 2}}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: "done"}},
	}
	data, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(dir, "ev_1.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: leaderboardIndex()}}
	state := func() any {
		code, body := reportCardServe(t, env, APIPrefix+"/evaluations/ev_1/report")
		if code != fasthttp.StatusOK {
			t.Fatalf("report %d %v", code, body)
		}
		cc, _ := body["cheat_check"].(map[string]any)
		return cc["state"]
	}
	if s := state(); s != "no_twins" {
		t.Fatalf("state %v", s)
	}
	twinDir := filepath.Join(root, string(projectfs.AreaBenchmarks), "coding-core-twins")
	if err := os.MkdirAll(twinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock, _ := json.Marshal(schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"t1-twin": {Status: schemas.TaskAdmitted}}})
	if err := os.WriteFile(filepath.Join(twinDir, "suite.lock"), lock, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := state(); s != "not_measured" {
		t.Fatalf("state with twins %v", s)
	}
}
