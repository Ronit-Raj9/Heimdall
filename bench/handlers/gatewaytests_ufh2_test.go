package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ufh2Suite has n ready tasks.
func ufh2Suite(n int) *fakeSuites {
	s := &schemas.Suite{ID: "big", Lock: &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{}}}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("t%d", i)
		s.TaskIDs = append(s.TaskIDs, id)
		s.Lock.Tasks[id] = schemas.TaskLock{Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}}
	}
	return &fakeSuites{ids: []string{"big"}, suites: map[string]*schemas.Suite{"big": s}}
}

// Every feature with a minimum: the form's default size (GET
// /gateway/features) reaches it, a size that cannot is refused by the
// draft handler with a plain sentence, and a size that can is not refused.
func TestFeatureTestNeedRefusesSizesThatCannotReachIt(t *testing.T) {
	cases := []struct {
		feature       string
		tasks, trials int
		refuse        bool
		note          string // a warning note expected in the draft
	}{
		{schemas.GatewayFeatureComplexityRouter, 3, 1, true, ""},
		{schemas.GatewayFeatureComplexityRouter, 4, 3, true, ""}, // 12 runs, but 4 paired tasks
		{schemas.GatewayFeatureComplexityRouter, 5, 2, false, ""},
		{schemas.GatewayFeaturePromptVersion, 5, 1, true, ""},
		{schemas.GatewayFeaturePromptVersion, 10, 1, false, ""},
		{schemas.GatewayFeatureSkillVersion, 3, 1, true, ""},
		{schemas.GatewayFeatureProviders, 9, 1, true, ""},
		{schemas.GatewayFeatureMCPTools, 3, 1, true, ""},
		{schemas.GatewayFeatureSemanticCache, 3, 3, true, ""},
		{schemas.GatewayFeatureSemanticCache, 5, 2, false, ""},
		{schemas.GatewayFeatureBudgetCurve, 3, 1, true, ""},
		{schemas.GatewayFeatureBudgetCurve, 5, 2, false, ""},
		// Model calls depend on the agent, so a short size is warned, not refused.
		{schemas.GatewayFeatureOverhead, 3, 1, false, "3 tasks make about 12 of the 50 model calls needed. Pick 13 tasks."},
		{schemas.GatewayFeatureOverhead, 13, 1, false, ""},
	}
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Suites: ufh2Suite(30)}}
	h := gatewayTestsRouter(env, nil)
	for _, c := range cases {
		name := fmt.Sprintf("%s/%dx%d", c.feature, c.tasks, c.trials)
		body := fmt.Sprintf(`{"feature":%q,"suite":"big@1","base":"agents/a.yaml","model":"m","task_count":%d,"trials":%d,"budget_usd":1,"options":{"budgets":"0.1,0.2","to":"m2","providers":"openai,azure"}}`, c.feature, c.tasks, c.trials)
		st, out := secretsCall(h, "POST", APIPrefix+"/gateway/tests/draft", body)
		refused := st == 400 && strings.Contains(out, "cannot give a result")
		if refused != c.refuse {
			t.Errorf("%s: refused %v, want %v: %d %s", name, refused, c.refuse, st, out)
		}
		if c.refuse && !strings.Contains(out, "Needs 10 runs per setup") {
			t.Errorf("%s: refusal does not say what is needed: %s", name, out)
		}
		if c.note != "" && !strings.Contains(out, c.note) {
			t.Errorf("%s: no warning %q: %d %s", name, c.note, st, out)
		}
	}

	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/features", "")
	var resp struct {
		Features []struct {
			ID   string         `json:"id"`
			Need gwfeature.Need `json:"need"`
		} `json:"features"`
	}
	if st != 200 || json.Unmarshal([]byte(out), &resp) != nil {
		t.Fatalf("features %d %s", st, out)
	}
	seen := 0
	for _, f := range resp.Features {
		n := f.Need
		if n.RunsPerSetup == 0 && n.ModelCalls == 0 {
			continue
		}
		seen++
		if c := n.Check(n.Tasks, n.Trials, true); c.Status != "ok" {
			t.Errorf("%s: default size %dx%d does not reach the need: %+v", f.ID, n.Tasks, n.Trials, c)
		}
	}
	if seen != 8 {
		t.Errorf("features with a need: %d, want 8", seen)
	}
}
