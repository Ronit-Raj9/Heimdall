package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

func init() {
	gwfeature.Register(gwfeature.Feature{ID: "uff4_test_tasks", Name: "Tasks echo", Draft: func(ctx context.Context, env gwfeature.DraftEnv, req gwfeature.DraftRequest) (*schemas.Experiment, []string, error) {
		return &schemas.Experiment{Suite: req.Suite, Base: req.Base, Arms: map[string]schemas.ArmSpec{"A": {Label: "today"}, "B": {Label: "changed"}},
			Conditions: []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{All: len(req.Tasks) == 0, IDs: req.Tasks}, Trials: 1}}}, nil, nil
	}})
}

// uff4Suite has 5 tasks of which t2, t3 and t5 are ready (BFCL-like: part ready).
func uff4Suite() *fakeSuites {
	s := &schemas.Suite{ID: "bfcl", TaskIDs: []string{"t1", "t2", "t3", "t4", "t5"}, Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{}}}
	for _, id := range []string{"t2", "t3", "t5"} {
		s.Lock.Tasks[id] = schemas.TaskLock{Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}}
	}
	s.Lock.Tasks["t1"] = schemas.TaskLock{Status: schemas.TaskAdmitted}
	return &fakeSuites{ids: []string{"bfcl"}, suites: map[string]*schemas.Suite{"bfcl": s}}
}

func uff4Draft(t *testing.T, body string) (int, string) {
	t.Helper()
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Suites: uff4Suite()}}
	return secretsCall(gatewayTestsRouter(env, nil), "POST", APIPrefix+"/gateway/tests/draft", body)
}

// The form's own payload (task_count and name, as featureTestDialog sends
// them) against the handler: only ready tasks, the first task_count of them,
// the name kept, and a note that says so.
func TestDraftFormTaskCountReadyTasksAndName(t *testing.T) {
	st, out := uff4Draft(t, `{"feature":"uff4_test_tasks","suite":"bfcl@1","base":"agents/mcploop.yaml","budget_usd":1,"trials":1,"task_count":2,"name":"zz-test-small"}`)
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var resp struct {
		Experiment schemas.Experiment `json:"experiment"`
		Notes      []string           `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	e := resp.Experiment
	if e.Name != "zz-test-small" || len(e.Conditions) != 1 || strings.Join(e.Conditions[0].Tasks.IDs, ",") != "t2,t3" || e.Conditions[0].Tasks.All {
		t.Fatalf("draft %+v", e)
	}
	if len(resp.Notes) != 1 || resp.Notes[0] != "Runs 2 of the 3 ready tasks; 2 tasks of this benchmark are not ready and are left out." {
		t.Fatalf("notes %q", resp.Notes)
	}
	// No task count: every ready task, still never a task that is not ready.
	st, out = uff4Draft(t, `{"feature":"uff4_test_tasks","suite":"bfcl@1","base":"a","budget_usd":1}`)
	if st != 200 || !strings.Contains(out, `"tasks":["t2","t3","t5"]`) {
		t.Fatalf("%d %s", st, out)
	}
	for _, body := range []string{
		`{"feature":"uff4_test_tasks","suite":"bfcl@1","base":"a","budget_usd":1,"task_count":201}`,
		`{"feature":"uff4_test_tasks","suite":"bfcl@1","base":"a","budget_usd":1,"name":"` + strings.Repeat("n", 121) + `"}`,
	} {
		if st, out := uff4Draft(t, body); st != 400 {
			t.Fatalf("%s: %d %s", body, st, out)
		}
	}
}
