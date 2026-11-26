package handlers

import (
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

func findingsCheckRouter(t *testing.T) *router.Router {
	t.Helper()
	idx := w441Index()
	for i := range idx.runs {
		idx.runs[i].Slot.Suite = "coding-core@1"
	}
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Index: idx}}
	r := router.New()
	registerFindings(env, r)
	return r
}

// A behaviour check that fired becomes a finding with the tasks and runs
// where it fired; the same ref again answers the existing finding.
func TestFindingsAddFromCheck(t *testing.T) {
	r := findingsCheckRouter(t)
	code, body := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"check","ref":"claimed_without_tests:exp1"}`))
	if code != fasthttp.StatusCreated {
		t.Fatalf("status %d %v", code, body)
	}
	if body["source"] != "check" || body["origin"] != "rule" || body["key"] != "check:claimed_without_tests:exp1" {
		t.Fatalf("finding %v", body)
	}
	runs, _ := body["runs"].([]any)
	if len(runs) != 1 || runs[0] != "r2" {
		t.Fatalf("runs %v", body["runs"])
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("tasks %v", body["tasks"])
	}
	if tk := tasks[0].(map[string]any); tk["suite"] != "coding-core" || tk["task"] != "t1" {
		t.Fatalf("task %v", tk)
	}
	if code, _ := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"check","ref":"claimed_without_tests:exp1"}`)); code != fasthttp.StatusOK {
		t.Fatalf("second add %d", code)
	}
}

func TestFindingsAddFromCheckRefusals(t *testing.T) {
	r := findingsCheckRouter(t)
	for _, c := range []struct {
		ref  string
		want int
	}{
		{"claimed_without_tests", fasthttp.StatusBadRequest},
		{"../x:exp1", fasthttp.StatusBadRequest},
		{"claimed_without_tests:nope", fasthttp.StatusNotFound},
		{"no_such_rule:exp1", fasthttp.StatusNotFound},
		{"reads_git_history:exp1", fasthttp.StatusUnprocessableEntity}, // never fired
	} {
		code, body := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"check","ref":"`+c.ref+`"}`))
		if code != c.want {
			t.Fatalf("ref %q: %d %v, want %d", c.ref, code, body, c.want)
		}
	}
}
