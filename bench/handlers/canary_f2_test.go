package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

func f2CanaryEnv(t *testing.T) (*canaryInitHandler, string) {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "agents"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "agents", "toy.yaml"), []byte("id: toy\n"), 0o644)
	adm := schemas.TaskLock{Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}}
	st := &schemas.Suite{ID: "hd-bfcl", Version: "1.0.0", TaskIDs: []string{"r1", "n1", "n2"},
		Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"r1": adm}}}
	env := OrgEnv{Cfg: &lib.Config{Root: root, RunsRoot: "runs"}, Services: Services{Suites: &fakeSuites{
		ids: []string{"hd-bfcl"}, suites: map[string]*schemas.Suite{"hd-bfcl": st}}}}
	return &canaryInitHandler{env: env}, root
}

const f2CanaryBody = `{"name":"drift","agent":"agents/toy.yaml","suite":"hd-bfcl@1.0.0","tasks":["r1"],"trials":1,"targets":[{"provider":"openai","model":"gpt-5.4-nano"}],"budget":{"per_run_usd":0.3,"per_day_usd":0.3},"schedule":{"cron":"0 6 * * *","timezone":"Asia/Calcutta"}}`

// PH5-2, PH5-9, PT-3 through the POST the drift monitor form sends.
func TestCanaryCreateReadyTasksCompatAndZone(t *testing.T) {
	h, root := f2CanaryEnv(t)
	for name, body := range map[string]string{
		"not ready": strings.Replace(f2CanaryBody, `["r1"]`, `["r1","n1"]`, 1),
		"all":       strings.Replace(f2CanaryBody, `["r1"]`, `"all"`, 1),
	} {
		ctx := schedCall(h.create, body, "")
		if ctx.Response.StatusCode() != 422 || !strings.Contains(string(ctx.Response.Body()), `"field":"tasks"`) {
			t.Fatalf("%s: %d %s", name, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	old := evalCompat
	evalCompat = func(*schemas.AgentConfig, *schemas.Suite) (bool, []schemas.Capability, string) {
		return false, nil, "Agent toy cannot run benchmark hd-bfcl: it only answers in text."
	}
	ctx := schedCall(h.create, f2CanaryBody, "")
	evalCompat = old
	if ctx.Response.StatusCode() != 422 || !strings.Contains(string(ctx.Response.Body()), "only answers in text") {
		t.Fatalf("incompatible pair: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = schedCall(h.create, f2CanaryBody, "")
	if ctx.Response.StatusCode() != 201 {
		t.Fatalf("ready task: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	d, err := canary.Load(canary.Path(root, "drift"))
	if err != nil || d.Schedule.Timezone != "Asia/Kolkata" {
		t.Fatalf("saved %+v %v", d, err)
	}
}
